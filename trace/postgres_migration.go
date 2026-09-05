package trace

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/yoanbernabeu/grepai/config"
)

func shouldMigrateGOB(symbolCount int, gobExists bool) bool { return symbolCount == 0 && gobExists }

func (s *PostgresSymbolStore) migrateGOBIfNeeded(ctx context.Context) error {
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM symbols WHERE project_id=$1`, s.projectID).Scan(&count); err != nil {
		return fmt.Errorf("failed to check symbol migration: %w", err)
	}
	path := config.GetSymbolIndexPath(s.projectRoot)
	_, statErr := os.Stat(path)
	if !shouldMigrateGOB(count, statErr == nil) {
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		return nil
	}
	log.Printf("trace: migrating GOB symbol index %s to Postgres", path)
	gobStore := NewGOBSymbolStore(path)
	if err := gobStore.Load(ctx); err != nil {
		return fmt.Errorf("failed to load GOB symbol index for migration: %w", err)
	}
	files := make([]string, 0, len(gobStore.fileIndex))
	for file := range gobStore.fileIndex {
		files = append(files, file)
	}
	// Group references by file once — scanning the full References map per file
	// would make migration O(files × totalRefs), which does not finish on
	// large indexes (hundreds of thousands of files, millions of refs).
	refsByFile := make(map[string][]Reference, len(files))
	for _, byName := range gobStore.index.References {
		for _, ref := range byName {
			refsByFile[ref.File] = append(refsByFile[ref.File], ref)
		}
	}
	for start := 0; start < len(files); start += 500 {
		end := start + 500
		if end > len(files) {
			end = len(files)
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		for _, file := range files[start:end] {
			symbols, _ := gobStore.GetSymbolsForFile(ctx, file)
			hash := gobStore.fileContentHashes[file]
			version := gobStore.fileExtractorVersions[file]
			if err = s.saveFileTx(ctx, tx, file, hash, &version, symbols, refsByFile[file]); err != nil {
				_ = tx.Rollback(ctx)
				s.clearProject(ctx)
				return fmt.Errorf("failed to migrate GOB symbol batch: %w", err)
			}
		}
		if err = tx.Commit(ctx); err != nil {
			s.clearProject(ctx)
			return fmt.Errorf("failed to commit GOB symbol batch: %w", err)
		}
	}
	backup := path + ".migrated.bak"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		s.clearProject(ctx)
		return fmt.Errorf("failed to archive migrated GOB symbol index: %w", err)
	}
	log.Printf("trace: migrated GOB symbol index to Postgres and archived it as %s", backup)
	return nil
}

func (s *PostgresSymbolStore) clearProject(ctx context.Context) {
	for _, table := range []string{"symbols", "refs", "call_edges", "symbol_files"} {
		_, _ = s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE project_id=$1`, s.projectID)
	}
}
