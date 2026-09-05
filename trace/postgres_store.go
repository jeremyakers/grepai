package trace

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresSymbolStore stores symbol and trace data incrementally in Postgres.
type PostgresSymbolStore struct {
	pool        *pgxpool.Pool
	projectID   string
	projectRoot string
}

func NewPostgresSymbolStore(ctx context.Context, dsn, projectID, projectRoot string) (*PostgresSymbolStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
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

func symbolSchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS symbol_files (project_id TEXT NOT NULL, path TEXT NOT NULL, content_hash TEXT NOT NULL DEFAULT '', extractor_version TEXT NOT NULL DEFAULT '', mod_time TIMESTAMPTZ NOT NULL, PRIMARY KEY(project_id, path))`,
		`ALTER TABLE symbol_files ADD COLUMN IF NOT EXISTS extractor_version TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS symbols (project_id TEXT NOT NULL, name TEXT NOT NULL, file TEXT NOT NULL, line INTEGER NOT NULL, end_line INTEGER NOT NULL DEFAULT 0, kind TEXT NOT NULL, signature TEXT NOT NULL DEFAULT '', receiver TEXT NOT NULL DEFAULT '', package_name TEXT NOT NULL DEFAULT '', exported BOOLEAN NOT NULL DEFAULT FALSE, language TEXT NOT NULL DEFAULT '', docstring TEXT NOT NULL DEFAULT '', feature_path TEXT NOT NULL DEFAULT '')`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS end_line INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS receiver TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS package_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS exported BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS docstring TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS feature_path TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_project_name ON symbols(project_id, name)`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_project_file ON symbols(project_id, file)`,
		`CREATE TABLE IF NOT EXISTS refs (project_id TEXT NOT NULL, symbol_name TEXT NOT NULL, file TEXT NOT NULL, line INTEGER NOT NULL, col INTEGER NOT NULL DEFAULT 0, ref_type TEXT NOT NULL DEFAULT '', context TEXT NOT NULL DEFAULT '', caller TEXT NOT NULL DEFAULT '', caller_file TEXT NOT NULL DEFAULT '', caller_line INTEGER NOT NULL DEFAULT 0)`,
		`ALTER TABLE refs ADD COLUMN IF NOT EXISTS caller_file TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE refs ADD COLUMN IF NOT EXISTS caller_line INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_refs_project_name ON refs(project_id, symbol_name)`,
		`CREATE INDEX IF NOT EXISTS idx_refs_project_file ON refs(project_id, file)`,
		`CREATE TABLE IF NOT EXISTS call_edges (project_id TEXT NOT NULL, caller TEXT NOT NULL, callee TEXT NOT NULL, file TEXT NOT NULL, line INTEGER NOT NULL, call_type TEXT NOT NULL DEFAULT '')`,
		`ALTER TABLE call_edges ADD COLUMN IF NOT EXISTS call_type TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_call_edges_project_caller ON call_edges(project_id, caller)`,
		`CREATE INDEX IF NOT EXISTS idx_call_edges_project_callee ON call_edges(project_id, callee)`,
		`CREATE INDEX IF NOT EXISTS idx_call_edges_project_file ON call_edges(project_id, file)`,
	}
}

func (s *PostgresSymbolStore) ensureSchema(ctx context.Context) error {
	for _, query := range symbolSchemaQueries() {
		if _, err := s.pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to execute symbol schema query: %w", err)
		}
	}
	return nil
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

func (s *PostgresSymbolStore) saveFile(ctx context.Context, filePath, contentHash string, extractorVersion *string, symbols []Symbol, refs []Reference) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin symbol file transaction: %w", err)
	}
	defer tx.Rollback(ctx)
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
		err := tx.QueryRow(ctx, `SELECT extractor_version FROM symbol_files WHERE project_id=$1 AND path=$2`, s.projectID, filePath).Scan(&version)
		if err != nil && err != pgx.ErrNoRows {
			return fmt.Errorf("failed to read existing extractor version: %w", err)
		}
	}
	if err := s.deleteFileTx(ctx, tx, filePath); err != nil {
		return err
	}
	if len(symbols) > 0 {
		rows := make([][]any, 0, len(symbols))
		for _, sym := range symbols {
			rows = append(rows, []any{s.projectID, sanUTF8(sym.Name), sanUTF8(sym.File), sym.Line, sym.EndLine, string(sym.Kind), sanUTF8(sym.Signature), sanUTF8(sym.Receiver), sanUTF8(sym.Package), sym.Exported, sanUTF8(sym.Language), sanUTF8(sym.Docstring), sanUTF8(sym.FeaturePath)})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"symbols"}, []string{"project_id", "name", "file", "line", "end_line", "kind", "signature", "receiver", "package_name", "exported", "language", "docstring", "feature_path"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("failed to insert symbols: %w", err)
		}
	}
	refRows := make([][]any, 0, len(refs))
	edgeRows := make([][]any, 0, len(refs))
	for _, ref := range refs {
		refRows = append(refRows, []any{s.projectID, sanUTF8(ref.SymbolName), sanUTF8(ref.File), ref.Line, ref.Column, sanUTF8(ref.Kind), sanUTF8(ref.Context), sanUTF8(ref.CallerName), sanUTF8(ref.CallerFile), ref.CallerLine})
		if ref.CallerName != "" && ref.CallerName != "<top-level>" {
			edgeRows = append(edgeRows, []any{s.projectID, sanUTF8(ref.CallerName), sanUTF8(ref.SymbolName), sanUTF8(ref.File), ref.Line, "direct"})
		}
	}
	if len(refRows) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"refs"}, []string{"project_id", "symbol_name", "file", "line", "col", "ref_type", "context", "caller", "caller_file", "caller_line"}, pgx.CopyFromRows(refRows)); err != nil {
			return fmt.Errorf("failed to insert references: %w", err)
		}
	}
	if len(edgeRows) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"call_edges"}, []string{"project_id", "caller", "callee", "file", "line", "call_type"}, pgx.CopyFromRows(edgeRows)); err != nil {
			return fmt.Errorf("failed to insert call edges: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO symbol_files (project_id,path,content_hash,extractor_version,mod_time) VALUES ($1,$2,$3,$4,$5)`, s.projectID, sanUTF8(filePath), contentHash, version, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to insert symbol file: %w", err)
	}
	return nil
}

// utf8 sanitizes a string for Postgres TEXT columns: real-world source files
// (and paths) can contain bytes that are not valid UTF-8, which Postgres
// rejects with SQLSTATE 22021. GOB files tolerate them; Postgres must not see
// them. Invalid sequences are replaced with U+FFFD.
func sanUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

func (s *PostgresSymbolStore) DeleteFile(ctx context.Context, filePath string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin delete transaction: %w", err)
	}
	defer tx.Rollback(ctx)
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
		if _, err := tx.Exec(ctx, query, s.projectID, filePath); err != nil {
			return fmt.Errorf("failed to delete symbol file data: %w", err)
		}
	}
	return nil
}

func (s *PostgresSymbolStore) IsFileIndexed(filePath string) bool {
	var exists bool
	err := s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM symbol_files WHERE project_id=$1 AND path=$2)`, s.projectID, filePath).Scan(&exists)
	return err == nil && exists
}

func (s *PostgresSymbolStore) GetFileContentHash(filePath string) (string, bool) {
	var value string
	err := s.pool.QueryRow(context.Background(), `SELECT content_hash FROM symbol_files WHERE project_id=$1 AND path=$2 AND content_hash<>''`, s.projectID, filePath).Scan(&value)
	return value, err == nil
}

func (s *PostgresSymbolStore) GetFileExtractorVersion(filePath string) (string, bool) {
	var value string
	err := s.pool.QueryRow(context.Background(), `SELECT extractor_version FROM symbol_files WHERE project_id=$1 AND path=$2 AND extractor_version<>''`, s.projectID, filePath).Scan(&value)
	return value, err == nil
}

func (s *PostgresSymbolStore) Load(ctx context.Context) error {
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
