package trace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yoanbernabeu/grepai/internal/fileutil"
)

// PostgresSymbolStore stores symbol and trace data incrementally in Postgres.
type PostgresSymbolStore struct {
	pool               *pgxpool.Pool
	projectID          string
	projectRoot        string
	migrationBatchHook func(int) error
	mutationHook       func(string, string) error
	schemaDDLHook      func(int, string) error
}

func NewPostgresSymbolStore(ctx context.Context, dsn, projectID, projectRoot string) (*PostgresSymbolStore, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to configure postgres: %w", err)
	}
	return newPostgresSymbolStoreWithPoolConfig(ctx, poolConfig, projectID, projectRoot)
}

func newPostgresSymbolStoreWithPoolConfig(ctx context.Context, poolConfig *pgxpool.Config, projectID, projectRoot string) (*PostgresSymbolStore, error) {
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}
	s := &PostgresSymbolStore{pool: pool, projectID: projectID, projectRoot: projectRoot}
	if err := s.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresSymbolStore) SaveFile(ctx context.Context, filePath string, symbols []Symbol, refs []Reference) error {
	return s.SaveFileWithContentHash(ctx, filePath, "", symbols, refs)
}

func (s *PostgresSymbolStore) SaveFileWithContentHash(ctx context.Context, filePath, contentHash string, symbols []Symbol, refs []Reference) error {
	return s.saveFile(ctx, filePath, contentHash, nil, symbols, refs)
}

func (s *PostgresSymbolStore) SaveFileWithSignature(ctx context.Context, filePath, contentHash, extractorVersion string, symbols []Symbol, refs []Reference) error {
	return s.saveFile(ctx, filePath, contentHash, &extractorVersion, symbols, refs)
}

func (s *PostgresSymbolStore) saveFile(ctx context.Context, filePath, contentHash string, extractorVersion *string, symbols []Symbol, refs []Reference) (retErr error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin symbol file transaction: %w", err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			retErr = errors.Join(retErr, fmt.Errorf("failed to rollback symbol file transaction: %w", rollbackErr))
		}
	}()
	if err := s.lockFileMutation(ctx, tx, "save", filePath); err != nil {
		return err
	}
	if err := s.saveFileTx(ctx, tx, filePath, contentHash, extractorVersion, symbols, refs); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit symbol file transaction: %w", err)
	}
	return nil
}

func (s *PostgresSymbolStore) saveFileTx(ctx context.Context, tx pgx.Tx, filePath, contentHash string, extractorVersion *string, symbols []Symbol, refs []Reference) error {
	version := ""
	if extractorVersion != nil {
		version = *extractorVersion
	} else {
		err := tx.QueryRow(ctx, `SELECT extractor_version FROM symbol_files WHERE project_id=$1 AND path=$2`, identityBytes(s.projectID), identityBytes(filePath)).Scan(&version)
		if err != nil && err != pgx.ErrNoRows {
			return fmt.Errorf("failed to read existing extractor version: %w", err)
		}
	}
	if err := s.deleteFileTx(ctx, tx, filePath); err != nil {
		return err
	}
	rows := buildPostgresFileRows(s.projectID, filePath, contentHash, version, symbols, refs, time.Now().UTC())
	return copyPostgresRows(ctx, tx, rows)
}

func identityBytes(s string) []byte { return []byte(s) }

// sanUTF8 sanitizes non-identity display values for Postgres TEXT columns.
func sanUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

func (s *PostgresSymbolStore) DeleteFile(ctx context.Context, filePath string) (retErr error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin delete transaction: %w", err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			retErr = errors.Join(retErr, fmt.Errorf("failed to rollback delete transaction: %w", rollbackErr))
		}
	}()
	if err := s.lockFileMutation(ctx, tx, "delete", filePath); err != nil {
		return err
	}
	if err := s.deleteFileTx(ctx, tx, filePath); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit delete transaction: %w", err)
	}
	return nil
}

func (s *PostgresSymbolStore) deleteFileTx(ctx context.Context, tx pgx.Tx, filePath string) error {
	for _, query := range []string{`DELETE FROM symbols WHERE project_id=$1 AND file=$2`, `DELETE FROM refs WHERE project_id=$1 AND file=$2`, `DELETE FROM call_edges WHERE project_id=$1 AND file=$2`, `DELETE FROM symbol_files WHERE project_id=$1 AND path=$2`} {
		if _, err := tx.Exec(ctx, query, identityBytes(s.projectID), identityBytes(filePath)); err != nil {
			return fmt.Errorf("failed to delete symbol file data: %w", err)
		}
	}
	return nil
}

func (s *PostgresSymbolStore) IsFileIndexed(filePath string) bool {
	var exists bool
	err := s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM symbol_files WHERE project_id=$1 AND path=$2)`, identityBytes(s.projectID), identityBytes(filePath)).Scan(&exists)
	return err == nil && exists
}

func (s *PostgresSymbolStore) GetFileContentHash(filePath string) (string, bool) {
	var value string
	err := s.pool.QueryRow(context.Background(), `SELECT content_hash FROM symbol_files WHERE project_id=$1 AND path=$2 AND content_hash<>''`, identityBytes(s.projectID), identityBytes(filePath)).Scan(&value)
	return value, err == nil
}

func (s *PostgresSymbolStore) GetFileExtractorVersion(filePath string) (string, bool) {
	var value string
	err := s.pool.QueryRow(context.Background(), `SELECT extractor_version FROM symbol_files WHERE project_id=$1 AND path=$2 AND extractor_version<>''`, identityBytes(s.projectID), identityBytes(filePath)).Scan(&value)
	return value, err == nil
}

func (s *PostgresSymbolStore) Load(ctx context.Context) (retErr error) {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	completed, err := s.migrationCompletedWithoutGOB(ctx)
	if err != nil {
		return err
	}
	if completed {
		return nil
	}
	writerLock, err := fileutil.AcquireProjectWriterLock(s.projectRoot)
	if err != nil {
		return fmt.Errorf("failed to acquire project writer lock for Postgres symbol migration: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, writerLock.Close()) }()
	return s.migrateGOBIfNeeded(ctx)
}

// LoadWithProjectWriterLockHeld loads migration state while the caller holds
// the project writer lock for the lifetime of this operation.
func (s *PostgresSymbolStore) LoadWithProjectWriterLockHeld(ctx context.Context) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	return s.migrateGOBIfNeeded(ctx)
}

// Persist is intentionally a no-op: every Postgres mutation is committed by
// SaveFile*/DeleteFile, so periodic persistence must not rewrite the index.
func (s *PostgresSymbolStore) Persist(context.Context) error { return nil }

func (s *PostgresSymbolStore) Close() error { s.pool.Close(); return nil }

var _ SymbolStore = (*PostgresSymbolStore)(nil)
