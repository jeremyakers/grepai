package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yoanbernabeu/grepai/framework"
	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/trace"
)

type legacyFileFingerprints interface {
	GetFileContentHash(string) (string, bool)
	GetFileExtractorVersion(string) (string, bool)
}

type initialSymbolFingerprints struct {
	snapshot map[string]trace.FileFingerprint
	legacy   legacyFileFingerprints
}

func loadInitialSymbolFingerprints(ctx context.Context, symbolStore trace.SymbolStore) (initialSymbolFingerprints, error) {
	snapshot, err := trace.LoadFileFingerprints(ctx, symbolStore)
	if err == nil {
		return initialSymbolFingerprints{snapshot: snapshot}, nil
	}
	if !errors.Is(err, trace.ErrFileFingerprintsUnsupported) {
		return initialSymbolFingerprints{}, err
	}
	legacy, ok := symbolStore.(legacyFileFingerprints)
	if !ok {
		return initialSymbolFingerprints{}, fmt.Errorf("symbol store does not support file fingerprints")
	}
	return initialSymbolFingerprints{legacy: legacy}, nil
}

func (f initialSymbolFingerprints) values(path string) (hash, version string, hashOK, versionOK bool) {
	if f.legacy != nil {
		hash, hashOK = f.legacy.GetFileContentHash(path)
		version, versionOK = f.legacy.GetFileExtractorVersion(path)
		return
	}
	value, ok := f.snapshot[path]
	if !ok {
		return "", "", false, false
	}
	return value.ContentHash, value.ExtractorVersion, value.HasContentHash, value.HasExtractorVersion
}

func runInitialScan(ctx context.Context, idx *indexer.Indexer, scanner *indexer.Scanner, extractor *trace.RegexExtractor, symbolStore trace.SymbolStore, tracedLanguages []string, lastIndexTime time.Time, background bool, onScan func(int, int, string), onEmbed func(indexer.BatchProgressInfo), processors ...*framework.ProcessorRegistry) (*indexer.IndexStats, error) {
	fingerprints, err := loadInitialSymbolFingerprints(ctx, symbolStore)
	if err != nil {
		return nil, err
	}
	announceInitialScan(background)
	stats, err := indexInitialFiles(ctx, idx, background, onScan, onEmbed)
	if err != nil {
		return nil, fmt.Errorf("initial indexing failed: %w", err)
	}
	announceInitialScanComplete(stats, background)
	if err := removeOfflineSymbolFiles(ctx, scanner, symbolStore, fingerprints.snapshot, stats.ScannedFiles); err != nil {
		return nil, err
	}
	if background {
		log.Println("Building symbol index...")
	} else {
		fmt.Println("Building symbol index...")
	}
	count, err := indexInitialSymbols(ctx, scanner, extractor, symbolStore, fingerprints, stats.ScannedFiles, stats.VerifiedUnchangedFiles, tracedLanguages, lastIndexTime, processors...)
	if err != nil {
		return nil, err
	}
	if err := symbolStore.Persist(ctx); err != nil {
		return nil, fmt.Errorf("persist symbol index: %w", err)
	}
	if background {
		log.Printf("Symbol index built: %d symbols extracted", count)
	} else {
		fmt.Printf("Symbol index built: %d symbols extracted\n", count)
	}
	return stats, nil
}

func indexInitialFiles(ctx context.Context, idx *indexer.Indexer, background bool, onScan func(int, int, string), onEmbed func(indexer.BatchProgressInfo)) (*indexer.IndexStats, error) {
	scanProgress := func(info indexer.ProgressInfo) {
		if onScan != nil {
			onScan(info.Current, info.Total, info.CurrentFile)
		} else if !background {
			printProgress(info.Current, info.Total, info.CurrentFile)
		}
	}
	embedProgress := func(info indexer.BatchProgressInfo) {
		if onEmbed != nil {
			onEmbed(info)
		} else if !background {
			printBatchProgress(info)
		}
	}
	stats, err := idx.IndexAllWithBatchProgress(ctx, scanProgress, embedProgress)
	if !background {
		watchProgressOutput.clear()
		fmt.Println()
	}
	return stats, err
}

func indexInitialSymbols(ctx context.Context, scanner *indexer.Scanner, extractor *trace.RegexExtractor, symbolStore trace.SymbolStore, fingerprints initialSymbolFingerprints, files []indexer.FileMeta, verified map[string]indexer.VerifiedFile, languages []string, lastIndexTime time.Time, processors ...*framework.ProcessorRegistry) (int, error) {
	_ = lastIndexTime // Deprecated: per-file exact observations govern correctness.
	count := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if !isTracedLanguage(strings.ToLower(filepath.Ext(file.Path)), languages) {
			continue
		}
		hash, version, hashOK, versionOK := fingerprints.values(file.Path)
		if known, ok := verified[file.Path]; ok && hashOK && versionOK && hash == known.Hash && version == extractor.Version() {
			fresh, statErr := scanner.StatFile(file.Path)
			if statErr == nil && fresh != nil && fresh.Size == known.Size && fresh.ObservedModTime.Equal(known.ModTime) {
				continue
			}
		}
		info, err := scanner.ScanFile(file.Path)
		if err != nil {
			log.Printf("Warning: failed to scan %s for symbols: %v", file.Path, err)
			continue
		}
		if info == nil {
			continue
		}
		hash, version, hashOK, versionOK = fingerprints.values(info.Path)
		if hashOK && versionOK && hash == info.Hash && version == extractor.Version() {
			continue
		}
		symbols, refs, err := extractSymbolsWithFramework(ctx, extractor, info.Path, info.Content, processors...)
		if err != nil {
			log.Printf("Warning: failed to extract symbols from %s: %v", info.Path, err)
			continue
		}
		err = symbolStore.SaveFileWithSignature(ctx, info.Path, info.Hash, extractor.Version(), symbols, refs)
		if err != nil {
			return count, fmt.Errorf("save symbols for %s: %w", info.Path, err)
		}
		count += len(symbols)
	}
	return count, nil
}

func removeOfflineSymbolFiles(ctx context.Context, scanner *indexer.Scanner, symbolStore trace.SymbolStore, snapshot map[string]trace.FileFingerprint, scanned []indexer.FileMeta) error {
	return removeOfflineSymbolFilesWithCaseRenames(ctx, scanner, symbolStore, snapshot, scanned, indexer.FindCaseRenameWitnesses)
}

type caseRenameFinder func(string, []string, []indexer.FileMeta) map[string]string

func removeOfflineSymbolFilesWithCaseRenames(ctx context.Context, scanner *indexer.Scanner, symbolStore trace.SymbolStore, snapshot map[string]trace.FileFingerprint, scanned []indexer.FileMeta, findCaseRenames caseRenameFinder) error {
	if snapshot == nil {
		return nil
	}
	root := scanner.Root()
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("symbol cleanup root unavailable: %w", err)
	}
	seen := make(map[string]struct{}, len(scanned))
	for _, file := range scanned {
		seen[file.Path] = struct{}{}
	}
	indexedPaths := make([]string, 0, len(snapshot))
	for path := range snapshot {
		indexedPaths = append(indexedPaths, path)
	}
	caseRenames := findCaseRenames(root, indexedPaths, scanned)
	var candidates []string
	for path := range snapshot {
		if _, ok := seen[path]; ok {
			continue
		}
		_, caseRenamed := caseRenames[path]
		if _, err := os.Lstat(filepath.Join(root, path)); os.IsNotExist(err) || caseRenamed {
			candidates = append(candidates, path)
		}
	}
	sort.Strings(candidates)
	for _, path := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, caseRenamed := caseRenames[path]
		if _, err := os.Lstat(filepath.Join(root, path)); (err == nil || !os.IsNotExist(err)) && !caseRenamed {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			return fmt.Errorf("symbol cleanup root lost before removing %s: %w", path, err)
		}
		if err := symbolStore.DeleteFile(ctx, path); err != nil {
			return fmt.Errorf("delete offline symbol file %s: %w", path, err)
		}
	}
	return nil
}

func announceInitialScan(background bool) {
	if background {
		log.Println("Performing initial scan...")
	} else {
		fmt.Println("\nPerforming initial scan...")
	}
}

func announceInitialScanComplete(stats *indexer.IndexStats, background bool) {
	format := "Initial scan complete: %d files indexed, %d chunks created, %d files removed, %d skipped (took %s)"
	args := []any{stats.FilesIndexed, stats.ChunksCreated, stats.FilesRemoved, stats.FilesSkipped, stats.Duration.Round(time.Millisecond)}
	if background {
		log.Printf(format, args...)
	} else {
		fmt.Printf(format+"\n", args...)
	}
}
