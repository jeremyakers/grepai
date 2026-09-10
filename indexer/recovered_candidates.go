package indexer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/yoanbernabeu/grepai/store"
)

type removalReconciliation struct {
	reeligible     []FileInfo
	reused         []recoveredVerification
	retiredAliases []RetiredAlias
}

type recoveredVerification struct {
	path     string
	verified VerifiedFile
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
	for _, recovered := range result.reused {
		verified := recovered.verified
		stats.ScannedFiles = append(stats.ScannedFiles, FileMeta{Path: recovered.path, Size: verified.Size, ModTime: verified.ModTime.Unix(), ObservedModTime: verified.ModTime})
		stats.VerifiedUnchangedFiles[recovered.path] = verified
		stats.ExcludedFiles = removeStringPath(stats.ExcludedFiles, recovered.path)
	}
	return nil
}

func (idx *Indexer) reconcileRecoveredCandidate(ctx context.Context, file *FileInfo, metadata store.DocumentMetadata) (fileScanDecision, error) {
	if metadata.HasChunks && metadata.Hash != "" && metadata.Hash == file.Hash {
		current, err := idx.store.GetDocument(ctx, file.Path)
		if err != nil {
			return fileScanDecision{}, fmt.Errorf("reload recovered document: %w", err)
		}
		if current != nil && len(current.ChunkIDs) > 0 && current.Hash == file.Hash {
			currentMetadata := store.DocumentMetadata{
				Path:            file.Path,
				Hash:            current.Hash,
				HasChunks:       true,
				ModTime:         current.ModTime,
				HasExactModTime: current.HasExactModTime,
			}
			if currentMetadata.HasExactModTime && currentMetadata.ModTime.Equal(file.ObservedModTime) {
				return verifiedFileDecision(file), nil
			}
			return idx.refreshMatchingDocument(ctx, file, &currentMetadata)
		}
		return idx.reconcileInvalidRecoveredRecord(ctx, file.Path)
	}
	return fileScanDecision{file: file}, nil
}

func (idx *Indexer) reconcileInvalidRecoveredRecord(ctx context.Context, path string) (fileScanDecision, error) {
	fresh, err := idx.scanner.ScanFile(path)
	if err != nil {
		return fileScanDecision{countAsSkipped: true, missingAfterWalk: errors.Is(err, fs.ErrNotExist)}, nil
	}
	if fresh == nil {
		return idx.nilSnapshotDecision(path), nil
	}
	if err := validateReeligibleFilePath(fresh, path); err != nil {
		return fileScanDecision{}, err
	}
	current, err := idx.store.GetDocument(ctx, path)
	if err != nil {
		return fileScanDecision{}, fmt.Errorf("re-observe recovered document: %w", err)
	}
	if current == nil || len(current.ChunkIDs) == 0 || current.Hash != fresh.Hash {
		return fileScanDecision{file: fresh}, nil
	}
	metadata := store.DocumentMetadata{
		Path:            path,
		Hash:            current.Hash,
		HasChunks:       true,
		ModTime:         current.ModTime,
		HasExactModTime: current.HasExactModTime,
	}
	if metadata.HasExactModTime && metadata.ModTime.Equal(fresh.ObservedModTime) {
		return verifiedFileDecision(fresh), nil
	}
	return idx.refreshMatchingDocument(ctx, fresh, &metadata)
}

func validateReeligibleFilePath(file *FileInfo, indexedPath string) error {
	expected := filepath.FromSlash(indexedPath)
	if file.Path != expected {
		return fmt.Errorf("indexed path %q changed to unexpected spelling %q during re-eligibility", expected, file.Path)
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
