package trace

import (
	"context"
	"fmt"
	"sort"
)

func symbolSchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS symbol_files (project_id BYTEA NOT NULL, path BYTEA NOT NULL, content_hash TEXT NOT NULL DEFAULT '', extractor_version TEXT NOT NULL DEFAULT '', mod_time TIMESTAMPTZ NOT NULL, PRIMARY KEY(project_id, path))`,
		`ALTER TABLE symbol_files ADD COLUMN IF NOT EXISTS extractor_version TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS symbols (project_id BYTEA NOT NULL, name BYTEA NOT NULL, file BYTEA NOT NULL, line INTEGER NOT NULL, end_line INTEGER NOT NULL DEFAULT 0, kind TEXT NOT NULL, signature TEXT NOT NULL DEFAULT '', receiver TEXT NOT NULL DEFAULT '', package_name TEXT NOT NULL DEFAULT '', exported BOOLEAN NOT NULL DEFAULT FALSE, language TEXT NOT NULL DEFAULT '', docstring TEXT NOT NULL DEFAULT '', feature_path TEXT NOT NULL DEFAULT '')`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS end_line INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS receiver TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS package_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS exported BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS docstring TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE symbols ADD COLUMN IF NOT EXISTS feature_path TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS refs (project_id BYTEA NOT NULL, symbol_name BYTEA NOT NULL, file BYTEA NOT NULL, line INTEGER NOT NULL, col INTEGER NOT NULL DEFAULT 0, ref_type TEXT NOT NULL DEFAULT '', context TEXT NOT NULL DEFAULT '', caller BYTEA NOT NULL DEFAULT ''::bytea, caller_file BYTEA NOT NULL DEFAULT ''::bytea, caller_line INTEGER NOT NULL DEFAULT 0)`,
		`ALTER TABLE refs ADD COLUMN IF NOT EXISTS caller_file BYTEA NOT NULL DEFAULT ''::bytea`,
		`ALTER TABLE refs ADD COLUMN IF NOT EXISTS caller_line INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE refs ADD COLUMN IF NOT EXISTS ordinal INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS call_edges (project_id BYTEA NOT NULL, caller BYTEA NOT NULL, callee BYTEA NOT NULL, file BYTEA NOT NULL, line INTEGER NOT NULL, call_type TEXT NOT NULL DEFAULT '')`,
		`ALTER TABLE call_edges ADD COLUMN IF NOT EXISTS call_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE call_edges ADD COLUMN IF NOT EXISTS ordinal INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS symbol_migrations (project_id BYTEA PRIMARY KEY, state TEXT NOT NULL, source_path BYTEA NOT NULL, source_digest BYTEA, source_size BIGINT, started_at TIMESTAMPTZ NOT NULL, completed_at TIMESTAMPTZ)`,
		`ALTER TABLE symbol_migrations ADD COLUMN IF NOT EXISTS source_digest BYTEA`,
		`ALTER TABLE symbol_migrations ADD COLUMN IF NOT EXISTS source_size BIGINT`,
	}
}

var identityColumns = map[string][]string{
	"call_edges":   {"project_id", "caller", "callee", "file"},
	"refs":         {"project_id", "symbol_name", "file", "caller", "caller_file"},
	"symbol_files": {"project_id", "path"},
	"symbols":      {"project_id", "name", "file"},
}

func identityColumnMigration(table, column string) string {
	return fmt.Sprintf(`DO $$ BEGIN IF (SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='%s' AND column_name='%s') <> 'bytea' THEN ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT; ALTER TABLE %s ALTER COLUMN %s TYPE BYTEA USING convert_to(%s, 'UTF8'); END IF; END $$`, table, column, table, column, table, column, column)
}

func (s *PostgresSymbolStore) ensureSchema(ctx context.Context) error {
	for _, query := range symbolSchemaQueries() {
		if _, err := s.pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to execute symbol schema query: %w", err)
		}
	}
	tables := make([]string, 0, len(identityColumns))
	for table := range identityColumns {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		for _, column := range identityColumns[table] {
			if _, err := s.pool.Exec(ctx, identityColumnMigration(table, column)); err != nil {
				return fmt.Errorf("failed to migrate symbol identity column: %w", err)
			}
		}
	}
	for _, query := range []string{
		`ALTER TABLE refs ALTER COLUMN caller SET DEFAULT ''::bytea`,
		`ALTER TABLE refs ALTER COLUMN caller_file SET DEFAULT ''::bytea`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_project_name ON symbols(project_id, name)`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_project_file ON symbols(project_id, file)`,
		`CREATE INDEX IF NOT EXISTS idx_refs_project_name ON refs(project_id, symbol_name)`,
		`CREATE INDEX IF NOT EXISTS idx_refs_project_file ON refs(project_id, file)`,
		`CREATE INDEX IF NOT EXISTS idx_call_edges_project_caller ON call_edges(project_id, caller)`,
		`CREATE INDEX IF NOT EXISTS idx_call_edges_project_callee ON call_edges(project_id, callee)`,
		`CREATE INDEX IF NOT EXISTS idx_call_edges_project_file ON call_edges(project_id, file)`,
	} {
		if _, err := s.pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to execute symbol index schema query: %w", err)
		}
	}
	return nil
}
