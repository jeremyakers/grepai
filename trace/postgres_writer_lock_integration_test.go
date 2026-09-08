package trace

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/yoanbernabeu/grepai/internal/fileutil"
)

type projectWriterLockHeldLoader interface {
	LoadWithProjectWriterLockHeld(context.Context) error
}

func TestPostgresMigrationRequiresProjectWriterLock(t *testing.T) {
	// Given a legacy GOB and a live watcher holding the project writer lock.
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	store := newIntegrationSymbolStore(t, "migration-writer-active", root)
	truncateSymbolTables(t, store)
	lock, err := fileutil.AcquireProjectWriterLock(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })

	// When a reader attempts the first migration.
	err = store.Load(ctx)

	// Then it reports the active writer without archiving or marking completion.
	var activeErr *fileutil.ProjectWriterActiveError
	if !errors.As(err, &activeErr) {
		t.Fatalf("Load error = %v, want ProjectWriterActiveError", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy GOB was touched: %v", err)
	}
	assertNoCompletedMigrationMarker(t, store)
}

func TestPostgresEmptyActivationRequiresProjectWriterLock(t *testing.T) {
	// Given no legacy GOB and a live watcher holding the project writer lock.
	ctx := context.Background()
	root := t.TempDir()
	store := newIntegrationSymbolStore(t, "empty-activation-writer-active", root)
	truncateSymbolTables(t, store)
	lock, err := fileutil.AcquireProjectWriterLock(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })

	// When a reader attempts the first empty activation.
	err = store.Load(ctx)

	// Then it reports the active writer and does not create the marker.
	var activeErr *fileutil.ProjectWriterActiveError
	if !errors.As(err, &activeErr) {
		t.Fatalf("Load error = %v, want ProjectWriterActiveError", err)
	}
	assertNoCompletedMigrationMarker(t, store)
}

func TestPostgresMigrationLoadsWithProjectWriterLockHeld(t *testing.T) {
	// Given a legacy GOB and the watcher already holding the writer lock.
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	store := newIntegrationSymbolStore(t, "migration-writer-held", root)
	truncateSymbolTables(t, store)
	lock, err := fileutil.AcquireProjectWriterLock(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	loader, ok := any(store).(projectWriterLockHeldLoader)
	if !ok {
		t.Fatal("PostgresSymbolStore does not expose lock-held loading")
	}

	// When the watcher invokes the lock-held load path.
	err = loader.LoadWithProjectWriterLockHeld(ctx)

	// Then migration succeeds without reacquiring the non-reentrant lock.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy GOB still exists: %v", err)
	}
}

func TestPostgresCompletedMigrationLoadRemainsConcurrentWithWriter(t *testing.T) {
	// Given a completed empty activation with no residual GOB.
	ctx := context.Background()
	root := t.TempDir()
	store := newIntegrationSymbolStore(t, "completed-reader-with-writer", root)
	truncateSymbolTables(t, store)
	if err := store.Load(ctx); err != nil {
		t.Fatal(err)
	}
	lock, err := fileutil.AcquireProjectWriterLock(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })

	// When a steady-state reader loads while the watcher lock is held.
	err = store.Load(ctx)

	// Then the completed/no-GOB fast path remains concurrent.
	if err != nil {
		t.Fatalf("steady-state Load failed: %v", err)
	}
}

func assertNoCompletedMigrationMarker(t *testing.T, store *PostgresSymbolStore) {
	t.Helper()
	var completed bool
	if err := store.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM symbol_migrations WHERE project_id=$1 AND state='completed')`, identityBytes(store.projectID)).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed {
		t.Fatal("completed migration marker was created")
	}
}
