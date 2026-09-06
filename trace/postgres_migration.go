package trace

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/internal/fileutil"
)

const migrationBatchSize = 500

func (s *PostgresSymbolStore) migrateGOBIfNeeded(ctx context.Context) (retErr error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration connection: %w", err)
	}
	defer conn.Release()
	key1, key2 := migrationAdvisoryKey(s.projectID)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1,$2)`, key1, key2); err != nil {
		return fmt.Errorf("failed to acquire symbol migration advisory lock: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, releaseMigrationAdvisoryLock(conn, key1, key2)) }()

	path := config.GetSymbolIndexPath(s.projectRoot)
	if err := fileutil.EnsureParentDir(path + ".lock"); err != nil {
		return fmt.Errorf("failed to create GOB migration lock directory: %w", err)
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open GOB migration lock: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, lockFile.Close()) }()
	if err := fileutil.FlockExclusive(lockFile, false); err != nil {
		return fmt.Errorf("failed to acquire exclusive GOB migration lock: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, fileutil.Funlock(lockFile)) }()

	status, err := s.migrationState(ctx, conn)
	if err != nil {
		return err
	}
	gobExists, err := fileExists(path)
	if err != nil {
		return err
	}
	if status.state == "completed" {
		if gobExists {
			fingerprint, err := fingerprintSource(path)
			if err != nil {
				return err
			}
			if err := verifyCompletedSource(status, fingerprint); err != nil {
				return err
			}
			return archiveMigratedGOB(path)
		}
		return nil
	}
	rows, err := s.projectDataRows(ctx, conn)
	if err != nil {
		return err
	}
	if rows > 0 {
		return fmt.Errorf("inconsistent partial Postgres symbol migration for project: %d data rows exist without a completed migration marker", rows)
	}
	if !gobExists {
		if status.state != "" {
			return fmt.Errorf("incomplete Postgres symbol migration has no source GOB file to retry")
		}
		if _, err := conn.Exec(ctx, `INSERT INTO symbol_migrations(project_id,state,source_path,source_digest,source_size,started_at,completed_at) VALUES($1,'completed',$2,NULL,NULL,NOW(),NOW())`, identityBytes(s.projectID), identityBytes(path)); err != nil {
			return fmt.Errorf("failed to activate empty Postgres symbol store: %w", err)
		}
		return nil
	}
	fingerprint, err := fingerprintSource(path)
	if err != nil {
		return err
	}

	gobStore, loaded, err := loadLockedGOBSymbolSnapshot(path)
	if err != nil {
		return fmt.Errorf("failed to load locked GOB symbol snapshot: %w", err)
	}
	if !loaded {
		return nil
	}
	if _, err := conn.Exec(ctx, `INSERT INTO symbol_migrations(project_id,state,source_path,source_digest,source_size,started_at,completed_at) VALUES($1,'migrating',$2,$3,$4,NOW(),NULL) ON CONFLICT(project_id) DO UPDATE SET state='migrating',source_path=EXCLUDED.source_path,source_digest=EXCLUDED.source_digest,source_size=EXCLUDED.source_size,started_at=EXCLUDED.started_at,completed_at=NULL`, identityBytes(s.projectID), identityBytes(path), fingerprint.digest, fingerprint.size); err != nil {
		return fmt.Errorf("failed to record symbol migration start: %w", err)
	}
	if err := s.importGOBSnapshot(ctx, conn, gobStore, fingerprint); err != nil {
		return err
	}
	if err := archiveMigratedGOB(path); err != nil {
		return err
	}
	log.Printf("trace: migrated GOB symbol index to Postgres and archived it as %s", path+".migrated.bak")
	return nil
}

func (s *PostgresSymbolStore) importGOBSnapshot(ctx context.Context, conn *pgxpool.Conn, gobStore *GOBSymbolStore, fingerprint sourceFingerprint) error {
	files := make([]string, 0, len(gobStore.fileIndex))
	for file := range gobStore.fileIndex {
		files = append(files, file)
	}
	sort.Strings(files)
	refsByFile := migrationRefsByFile(gobStore)
	symbolsByFile := migrationSymbolsByFile(gobStore)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin GOB symbol migration: %w", err)
	}
	started := time.Now()
	for start, batch := 0, 0; start < len(files); start, batch = start+migrationBatchSize, batch+1 {
		end := min(start+migrationBatchSize, len(files))
		batchFiles := make([]migrationFileRows, 0, end-start)
		for _, file := range files[start:end] {
			batchFiles = append(batchFiles, migrationFileRows{filePath: file, contentHash: gobStore.fileContentHashes[file], extractorVersion: gobStore.fileExtractorVersions[file], symbols: symbolsByFile[file], refs: refsByFile[file], modTime: time.Now().UTC()})
		}
		if err := copyMigrationFileBatch(ctx, tx, s.projectID, batchFiles); err != nil {
			return rollbackMigration(tx, fmt.Errorf("failed to import GOB symbol batch: %w", err))
		}
		if s.migrationBatchHook != nil {
			if err := s.migrationBatchHook(batch); err != nil {
				return rollbackMigration(tx, fmt.Errorf("GOB symbol migration batch hook failed: %w", err))
			}
		}
		log.Printf("trace: Postgres symbol migration progress: %d/%d files, batch %d, elapsed %s", end, len(files), batch+1, time.Since(started).Round(time.Second))
	}
	if _, err := tx.Exec(ctx, `UPDATE symbol_migrations SET state='completed',source_digest=$2,source_size=$3,completed_at=NOW() WHERE project_id=$1`, identityBytes(s.projectID), fingerprint.digest, fingerprint.size); err != nil {
		return rollbackMigration(tx, fmt.Errorf("failed to complete symbol migration marker: %w", err))
	}
	if err := tx.Commit(ctx); err != nil {
		return rollbackMigration(tx, fmt.Errorf("failed to commit GOB symbol migration: %w", err))
	}
	return nil
}

func rollbackMigration(tx pgx.Tx, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := tx.Rollback(ctx)
	if errors.Is(err, pgx.ErrTxClosed) {
		err = nil
	}
	return errors.Join(cause, err)
}
