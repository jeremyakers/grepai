package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/trace"
)

func removeOfflineSymbolFiles(ctx context.Context, scanner *indexer.Scanner, symbolStore trace.SymbolStore, snapshot map[string]trace.FileFingerprint, scanned []indexer.FileMeta, excluded []string) error {
	_, err := removeOfflineSymbolFilesForScan(ctx, scanner, symbolStore, snapshot, scanned, excluded)
	return err
}

func removeOfflineSymbolFilesForScan(ctx context.Context, scanner *indexer.Scanner, symbolStore trace.SymbolStore, snapshot map[string]trace.FileFingerprint, scanned []indexer.FileMeta, excluded []string) ([]reeligibleSymbolFile, error) {
	return removeOfflineSymbolFilesForScanWithSeams(ctx, scanner, symbolStore, snapshot, scanned, excluded, indexer.FindCaseRenameWitnesses, scanner.InspectExistingPath)
}

type caseRenameFinder func(string, []string, []indexer.FileMeta) map[string]string
type existingPathInspector func(string) (*indexer.FileInfo, indexer.PathExclusionReason, error)
type reeligibleSymbolFile struct {
	file       indexer.FileInfo
	staleAlias string
}

func removeOfflineSymbolFilesWithCaseRenames(ctx context.Context, scanner *indexer.Scanner, symbolStore trace.SymbolStore, snapshot map[string]trace.FileFingerprint, scanned []indexer.FileMeta, excluded []string, findCaseRenames caseRenameFinder) error {
	_, err := removeOfflineSymbolFilesForScanWithSeams(ctx, scanner, symbolStore, snapshot, scanned, excluded, findCaseRenames, scanner.InspectExistingPath)
	return err
}

func removeOfflineSymbolFilesForScanWithSeams(ctx context.Context, scanner *indexer.Scanner, symbolStore trace.SymbolStore, snapshot map[string]trace.FileFingerprint, scanned []indexer.FileMeta, excluded []string, findCaseRenames caseRenameFinder, inspect existingPathInspector) ([]reeligibleSymbolFile, error) {
	if snapshot == nil {
		return nil, nil
	}
	root := scanner.Root()
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("symbol cleanup root unavailable: %w", err)
	}
	seen := make(map[string]struct{}, len(scanned))
	for _, file := range scanned {
		seen[file.Path] = struct{}{}
	}
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, path := range excluded {
		excludedSet[path] = struct{}{}
	}
	indexedPaths := make([]string, 0, len(snapshot))
	for path := range snapshot {
		indexedPaths = append(indexedPaths, path)
	}
	caseRenames := findCaseRenames(root, indexedPaths, scanned)
	var candidates []string
	for path := range snapshot {
		_, intentionallyExcluded := excludedSet[path]
		if _, ok := seen[path]; ok && !intentionallyExcluded {
			continue
		}
		_, caseRenamed := caseRenames[path]
		_, statErr := os.Lstat(filepath.Join(root, path))
		remove := os.IsNotExist(statErr) || caseRenamed || intentionallyExcluded
		if statErr == nil && !remove {
			reason, err := scanner.ExistingPathExclusion(path)
			remove = err == nil && reason != ""
		}
		if remove {
			candidates = append(candidates, path)
		}
	}
	sort.Strings(candidates)
	var reeligible []reeligibleSymbolFile
	for _, path := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		caseWitness, caseRenamed := caseRenames[path]
		_, statErr := os.Lstat(filepath.Join(root, path))
		forced := caseRenamed
		if caseRenamed {
			current, err := indexer.RevalidateCaseRenameWitness(root, path, caseWitness)
			if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("revalidate symbol case rename %s to %s: %w", path, caseWitness, err)
			}
			if !current {
				file, reason, inspectErr := inspect(path)
				if inspectErr != nil && !os.IsNotExist(inspectErr) {
					return nil, fmt.Errorf("inspect reversed symbol case rename %s: %w", path, inspectErr)
				}
				caseRenamed = false
				forced = reason != ""
				if file != nil && reason == "" {
					retireAlias, retireErr := indexer.CanRetireCaseAlias(root, path, caseWitness)
					if retireErr != nil {
						return nil, fmt.Errorf("verify temporary symbol case alias %s: %w", caseWitness, retireErr)
					}
					recovered := reeligibleSymbolFile{file: *file}
					if retireAlias {
						if err := verifyInitialScanRoot(root); err != nil {
							return nil, fmt.Errorf("symbol cleanup root lost before reconciling reversed case rename %s: %w", path, err)
						}
						if err := symbolStore.DeleteFile(ctx, caseWitness); err != nil {
							return nil, fmt.Errorf("delete temporary symbol case alias %s: %w", caseWitness, err)
						}
						recovered.staleAlias = caseWitness
					}
					reeligible = append(reeligible, recovered)
					continue
				}
				if forced {
					retireAlias, retireErr := indexer.CanRetireCaseAlias(root, path, caseWitness)
					if retireErr != nil {
						return nil, fmt.Errorf("verify temporary excluded symbol case alias %s: %w", caseWitness, retireErr)
					}
					if retireAlias {
						if err := verifyInitialScanRoot(root); err != nil {
							return nil, fmt.Errorf("symbol cleanup root lost before removing excluded case alias %s: %w", caseWitness, err)
						}
						if err := symbolStore.DeleteFile(ctx, caseWitness); err != nil {
							return nil, fmt.Errorf("delete temporary excluded symbol case alias %s: %w", caseWitness, err)
						}
					}
				}
			}
		}
		if statErr == nil && !caseRenamed {
			file, reason, inspectErr := inspect(path)
			if inspectErr != nil {
				continue
			}
			if reason == "" && file != nil {
				reeligible = append(reeligible, reeligibleSymbolFile{file: *file})
				continue
			}
			forced = reason != ""
		}
		if (statErr == nil || !os.IsNotExist(statErr)) && !forced {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			return nil, fmt.Errorf("symbol cleanup root lost before removing %s: %w", path, err)
		}
		if err := symbolStore.DeleteFile(ctx, path); err != nil {
			return nil, fmt.Errorf("delete offline symbol file %s: %w", path, err)
		}
	}
	return reeligible, nil
}
