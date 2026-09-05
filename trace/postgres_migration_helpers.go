package trace

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func releaseMigrationAdvisoryLock(conn *pgxpool.Conn, key1, key2 int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1,$2)`, key1, key2).Scan(&unlocked); err != nil {
		return fmt.Errorf("failed to release symbol migration advisory lock: %w", err)
	}
	if !unlocked {
		return fmt.Errorf("symbol migration advisory lock was not held during release")
	}
	return nil
}

func (s *PostgresSymbolStore) migrationState(ctx context.Context, conn *pgxpool.Conn) (string, error) {
	var state string
	err := conn.QueryRow(ctx, `SELECT state FROM symbol_migrations WHERE project_id=$1`, identityBytes(s.projectID)).Scan(&state)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read symbol migration state: %w", err)
	}
	return state, nil
}

func (s *PostgresSymbolStore) projectDataRows(ctx context.Context, conn *pgxpool.Conn) (int64, error) {
	var rows int64
	err := conn.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM symbols WHERE project_id=$1)+(SELECT COUNT(*) FROM refs WHERE project_id=$1)+(SELECT COUNT(*) FROM call_edges WHERE project_id=$1)+(SELECT COUNT(*) FROM symbol_files WHERE project_id=$1)`, identityBytes(s.projectID)).Scan(&rows)
	if err != nil {
		return 0, fmt.Errorf("failed to check Postgres symbol migration eligibility: %w", err)
	}
	return rows, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to inspect GOB symbol index: %w", err)
}

func archiveMigratedGOB(path string) error {
	backup := path + ".migrated.bak"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Postgres symbol migration completed, but old backup could not be removed: %w", err)
	}
	if err := os.Rename(path, backup); err != nil {
		return fmt.Errorf("Postgres symbol migration completed, but source GOB could not be archived as %s: %w", backup, err)
	}
	return nil
}
