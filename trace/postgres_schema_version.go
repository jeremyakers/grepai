package trace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const currentSymbolSchemaVersion = 1
const symbolSchemaVersionQuery = `SELECT value FROM symbol_store_meta WHERE key='schema_version'`

type symbolSchemaVersionReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readSymbolSchemaVersion(ctx context.Context, reader symbolSchemaVersionReader) (version int, tableMissing bool, err error) {
	err = reader.QueryRow(ctx, symbolSchemaVersionQuery).Scan(&version)
	if err == nil {
		return version, false, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		return 0, true, nil
	}
	return 0, false, fmt.Errorf("failed to read symbol schema version: %w", err)
}

func schemaAdvisoryKey() (int32, int32) {
	return advisoryKey("grepai:symbol-schema")
}

func (s *PostgresSymbolStore) ensureSchema(ctx context.Context) (retErr error) {
	version, _, err := readSymbolSchemaVersion(ctx, s.pool)
	if err != nil {
		return err
	}
	if version == currentSymbolSchemaVersion {
		return nil
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire symbol schema connection: %w", err)
	}
	defer conn.Release()
	key1, key2 := schemaAdvisoryKey()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1,$2)`, key1, key2); err != nil {
		return fmt.Errorf("failed to acquire symbol schema advisory lock: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, releaseSchemaAdvisoryLock(conn, key1, key2)) }()
	version, _, err = readSymbolSchemaVersion(ctx, conn)
	if err != nil {
		return err
	}
	if version == currentSymbolSchemaVersion {
		return nil
	}
	if err := runSymbolSchemaDDL(ctx, conn, s.schemaDDLHook); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `INSERT INTO symbol_store_meta(key,value) VALUES('schema_version',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, currentSymbolSchemaVersion); err != nil {
		return fmt.Errorf("failed to record symbol schema version: %w", err)
	}
	return nil
}

func releaseSchemaAdvisoryLock(conn *pgxpool.Conn, key1, key2 int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1,$2)`, key1, key2).Scan(&unlocked); err != nil {
		return fmt.Errorf("failed to release symbol schema advisory lock: %w", err)
	}
	if !unlocked {
		return fmt.Errorf("symbol schema advisory lock was not held during release")
	}
	return nil
}
