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
	pool               *pgxpool.Pool
	projectID          string
	projectRoot        string
	migrationBatchHook func(int) error
	mutationHook       func(string, string) error
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
	if len(symbols) > 0 {
		rows := make([][]any, 0, len(symbols))
		for _, sym := range symbols {
			rows = append(rows, []any{identityBytes(s.projectID), identityBytes(sym.Name), identityBytes(sym.File), sym.Line, sym.EndLine, sanUTF8(string(sym.Kind)), sanUTF8(sym.Signature), sanUTF8(sym.Receiver), sanUTF8(sym.Package), sym.Exported, sanUTF8(sym.Language), sanUTF8(sym.Docstring), sanUTF8(sym.FeaturePath)})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"symbols"}, []string{"project_id", "name", "file", "line", "end_line", "kind", "signature", "receiver", "package_name", "exported", "language", "docstring", "feature_path"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("failed to insert symbols: %w", err)
		}
	}
	refRows := make([][]any, 0, len(refs))
	edgeRows := make([][]any, 0, len(refs))
	for ordinal, ref := range refs {
		refRows = append(refRows, []any{identityBytes(s.projectID), identityBytes(ref.SymbolName), identityBytes(ref.File), ref.Line, ref.Column, sanUTF8(ref.Kind), sanUTF8(ref.Context), identityBytes(ref.CallerName), identityBytes(ref.CallerFile), ref.CallerLine, ordinal})
		if ref.CallerName != "" && ref.CallerName != "<top-level>" {
			edgeRows = append(edgeRows, []any{identityBytes(s.projectID), identityBytes(ref.CallerName), identityBytes(ref.SymbolName), identityBytes(ref.File), ref.Line, "direct", ordinal})
		}
	}
	if len(refRows) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"refs"}, []string{"project_id", "symbol_name", "file", "line", "col", "ref_type", "context", "caller", "caller_file", "caller_line", "ordinal"}, pgx.CopyFromRows(refRows)); err != nil {
			return fmt.Errorf("failed to insert references: %w", err)
		}
	}
	if len(edgeRows) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"call_edges"}, []string{"project_id", "caller", "callee", "file", "line", "call_type", "ordinal"}, pgx.CopyFromRows(edgeRows)); err != nil {
			return fmt.Errorf("failed to insert call edges: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO symbol_files (project_id,path,content_hash,extractor_version,mod_time) VALUES ($1,$2,$3,$4,$5)`, identityBytes(s.projectID), identityBytes(filePath), sanUTF8(contentHash), sanUTF8(version), time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to insert symbol file: %w", err)
	}
	return nil
}

func identityBytes(s string) []byte { return []byte(s) }

// sanUTF8 sanitizes non-identity display values for Postgres TEXT columns.
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
