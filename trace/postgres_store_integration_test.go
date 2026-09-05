package trace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
)

func newIntegrationSymbolStore(t *testing.T, projectID, projectRoot string) *PostgresSymbolStore {
	t.Helper()
	dsn := os.Getenv("GREPAI_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("GREPAI_POSTGRES_TEST_DSN is not set")
	}
	store, err := NewPostgresSymbolStore(context.Background(), dsn, projectID, projectRoot)
	if err != nil {
		t.Fatalf("failed to create Postgres symbol store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func truncateSymbolTables(t *testing.T, store *PostgresSymbolStore) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE TABLE symbols, refs, call_edges, symbol_files, symbol_migrations`); err != nil {
		t.Fatalf("failed to truncate symbol tables: %v", err)
	}
}

func TestPostgresSymbolStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newIntegrationSymbolStore(t, "round-trip", t.TempDir())
	truncateSymbolTables(t, store)
	if err := store.Load(ctx); err != nil {
		t.Fatalf("failed to activate empty Postgres symbol store: %v", err)
	}

	aSymbols := []Symbol{{Name: "A", Kind: KindFunction, File: "a.go", Line: 1, EndLine: 8, Signature: "func A()", Package: "sample", Exported: true, Language: "go"}}
	aRefs := []Reference{
		{SymbolName: "B", Kind: RefKindCall, File: "a.go", Line: 3, Column: 2, Context: "B()", CallerName: "A", CallerFile: "a.go", CallerLine: 1},
		{SymbolName: "value", Kind: RefKindRead, File: "a.go", Line: 4, CallerName: "A", CallerFile: "a.go", CallerLine: 1},
		{SymbolName: "value", Kind: RefKindWrite, File: "a.go", Line: 5, CallerName: "A", CallerFile: "a.go", CallerLine: 1},
	}
	if err := store.SaveFileWithContentHash(ctx, "a.go", "hash-a", aSymbols, aRefs); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFileWithSignature(ctx, "b.go", "hash-b", "extractor-v1", []Symbol{{Name: "B", Kind: KindFunction, File: "b.go", Line: 1}}, []Reference{{SymbolName: "C", Kind: RefKindCall, File: "b.go", Line: 3, CallerName: "B", CallerFile: "b.go", CallerLine: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFile(ctx, "c.go", []Symbol{{Name: "C", Kind: KindFunction, File: "c.go", Line: 1}}, nil); err != nil {
		t.Fatal(err)
	}

	if got, _ := store.LookupSymbol(ctx, "A"); len(got) != 1 || got[0].Signature != "func A()" {
		t.Fatalf("LookupSymbol = %#v", got)
	}
	batch, err := store.LookupSymbolsBatch(ctx, []string{"A", "B", "Missing", "A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 || len(batch["A"]) != 1 || len(batch["B"]) != 1 {
		t.Fatalf("LookupSymbolsBatch = %#v", batch)
	}
	if _, ok := batch["Missing"]; ok {
		t.Fatalf("missing name should not be present: %#v", batch)
	}
	if empty, err := store.LookupSymbolsBatch(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty LookupSymbolsBatch = %#v, %v", empty, err)
	}
	if got, _ := store.LookupCallers(ctx, "B"); len(got) != 1 || got[0].CallerName != "A" {
		t.Fatalf("LookupCallers = %#v", got)
	}
	if got, _ := store.LookupCallees(ctx, "A", "a.go"); len(got) != 1 || got[0].SymbolName != "B" {
		t.Fatalf("LookupCallees = %#v", got)
	}
	if got, _ := store.LookupReaders(ctx, "value"); len(got) != 1 {
		t.Fatalf("LookupReaders = %#v", got)
	}
	if got, _ := store.LookupWriters(ctx, "value"); len(got) != 1 {
		t.Fatalf("LookupWriters = %#v", got)
	}
	if got, _ := store.GetSymbolsForFile(ctx, "a.go"); len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("GetSymbolsForFile = %#v", got)
	}
	graph, err := store.GetCallGraph(ctx, "A", 2)
	if err != nil {
		t.Fatal(err)
	}
	// GOBSymbolStore builds call-graph edges from every ref with a CallerName,
	// including read/write refs (it does not filter by ref kind), so the
	// faithful result here is 3 nodes and 3 edges: A->B, A->value (a read
	// ref), B->C. Postgres must match that behavior exactly.
	if len(graph.Nodes) != 3 || len(graph.Edges) != 3 {
		t.Fatalf("GetCallGraph nodes=%v edges=%v", graph.Nodes, graph.Edges)
	}
	if hash, ok := store.GetFileContentHash("a.go"); !ok || hash != "hash-a" {
		t.Fatalf("content hash = %q, %v", hash, ok)
	}

	// Real-world indexes can contain non-UTF-8 bytes (GOB tolerates them,
	// Postgres rejects with SQLSTATE 22021) — they must be sanitized, not fatal.
	badRef := Reference{SymbolName: "weird", Kind: RefKindCall, File: "bad.go", Line: 1, Context: string([]byte{0xe0, 0x2e, 0x2e}), CallerName: "A", CallerFile: "a.go", CallerLine: 1}
	if err := store.SaveFileWithContentHash(ctx, "bad.go", "hash-bad", nil, []Reference{badRef}); err != nil {
		t.Fatalf("SaveFile with invalid UTF-8 failed: %v", err)
	}
	got, err := store.LookupCallers(ctx, "weird")
	if err != nil || len(got) != 1 {
		t.Fatalf("LookupCallers after UTF-8 sanitize = %#v, %v", got, err)
	}
	if got[0].Context != "�.." {
		t.Fatalf("invalid UTF-8 was not replaced: %q", got[0].Context)
	}
	if err := store.SaveFileWithContentHash(ctx, "b.go", "hash-b2", []Symbol{{Name: "B", Kind: KindFunction, File: "b.go", Line: 1}}, []Reference{{SymbolName: "C", Kind: RefKindCall, File: "b.go", Line: 3, CallerName: "B", CallerFile: "b.go", CallerLine: 1}}); err != nil {
		t.Fatal(err)
	}
	if version, ok := store.GetFileExtractorVersion("b.go"); !ok || version != "extractor-v1" {
		t.Fatalf("extractor version was not preserved: %q, %v", version, ok)
	}

	if err := store.DeleteFile(ctx, "a.go"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetSymbolsForFile(ctx, "a.go"); len(got) != 0 {
		t.Fatalf("deleted symbols remain: %#v", got)
	}
	if got, _ := store.LookupSymbol(ctx, "B"); len(got) != 1 {
		t.Fatal("DeleteFile removed another file")
	}
	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// bad.go (from the UTF-8 case above) contributes no symbols, 1 ref, 1 file.
	if stats.TotalFiles != 3 || stats.TotalSymbols != 2 || stats.TotalReferences != 2 {
		t.Fatalf("stats = %#v", stats)
	}
	if stats.IndexSize <= 0 {
		t.Fatalf("expected positive project logical IndexSize, got %d", stats.IndexSize)
	}
}

func TestPostgresSymbolStoreLosslessIdentityBytes(t *testing.T) {
	ctx := context.Background()
	store := newIntegrationSymbolStore(t, "identity-bytes", t.TempDir())
	truncateSymbolTables(t, store)
	path1, path2 := "src/\xff.go", "src/\xfe.go"
	name1, name2 := "Thing\xff", "Thing\xfe"
	badContext := "show:\xff"
	for _, item := range []struct{ path, name string }{{path1, name1}, {path2, name2}} {
		ref := Reference{SymbolName: item.name, Kind: RefKindCall, File: item.path, Line: 2, Context: badContext, CallerName: item.name, CallerFile: item.path, CallerLine: 1}
		if err := store.SaveFile(ctx, item.path, []Symbol{{Name: item.name, File: item.path, Line: 1}}, []Reference{ref}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ path, name string }{{path1, name1}, {path2, name2}} {
		syms, err := store.LookupSymbol(ctx, item.name)
		if err != nil || len(syms) != 1 || syms[0].Name != item.name || syms[0].File != item.path {
			t.Fatalf("lossless identity lookup for %q = %#v, %v", item.name, syms, err)
		}
		byFile, err := store.GetSymbolsForFile(ctx, item.path)
		if err != nil || len(byFile) != 1 || byFile[0].Name != item.name {
			t.Fatalf("lossless file lookup for %q = %#v, %v", item.path, byFile, err)
		}
		refs, err := store.LookupCallers(ctx, item.name)
		if err != nil || len(refs) != 1 || refs[0].Context != "show:�" {
			t.Fatalf("display sanitation for %q = %#v, %v", item.name, refs, err)
		}
	}
	if err := store.DeleteFile(ctx, path1); err != nil {
		t.Fatal(err)
	}
	if store.IsFileIndexed(path1) || !store.IsFileIndexed(path2) {
		t.Fatal("distinct invalid-byte paths were not isolated during delete")
	}
}

func TestPostgresLookupCalleesMatchesGOB(t *testing.T) {
	ctx := context.Background()
	pg := newIntegrationSymbolStore(t, "callees-parity", t.TempDir())
	truncateSymbolTables(t, pg)
	gob := NewGOBSymbolStore(filepath.Join(t.TempDir(), "symbols.gob"))
	refs := []Reference{
		{SymbolName: "readOnly", Kind: RefKindRead, File: "main.go", Line: 10, CallerName: "Main"},
		{SymbolName: "CallAtSameSite", Kind: RefKindCall, File: "main.go", Line: 10, CallerName: "Main"},
		{SymbolName: "writeOnly", Kind: RefKindWrite, File: "main.go", Line: 11, CallerName: "Main"},
		{SymbolName: "Called", Kind: RefKindCall, File: "main.go", Line: 12, CallerName: "Main"},
		{SymbolName: "Legacy", Kind: "", File: "main.go", Line: 13, CallerName: "Main"},
	}
	for _, store := range []SymbolStore{gob, pg} {
		if err := store.SaveFile(ctx, "main.go", []Symbol{{Name: "Main", File: "main.go", Line: 1}}, refs); err != nil {
			t.Fatal(err)
		}
	}
	want, err := gob.LookupCallees(ctx, "Main", "main.go")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pg.LookupCallees(ctx, "Main", "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Postgres callees differ from GOB:\n got: %#v\nwant: %#v", got, want)
	}
	if err := pg.DeleteFile(ctx, "main.go"); err != nil {
		t.Fatal(err)
	}
	if err := pg.SaveFile(ctx, "main.go", []Symbol{{Name: "Main", File: "main.go", Line: 1}}, refs); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`VACUUM FULL refs`, `VACUUM FULL call_edges`} {
		if _, err := pg.pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	rewritten, err := pg.LookupCallees(ctx, "Main", "main.go")
	if err != nil || !reflect.DeepEqual(rewritten, want) {
		t.Fatalf("durable ordinal parity after rewrite = %#v, %v; want %#v", rewritten, err, want)
	}
}

func TestPostgresSymbolStoreTenantIsolation(t *testing.T) {
	ctx := context.Background()
	one := newIntegrationSymbolStore(t, "tenant-one", t.TempDir())
	truncateSymbolTables(t, one)
	two := newIntegrationSymbolStore(t, "tenant-two", t.TempDir())
	if err := one.SaveFile(ctx, "same.go", []Symbol{{Name: "OnlyOne", File: "same.go", Line: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := two.SaveFile(ctx, "same.go", []Symbol{{Name: "OnlyTwo", File: "same.go", Line: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := one.LookupSymbol(ctx, "OnlyTwo"); len(got) != 0 {
		t.Fatalf("cross-tenant symbol visible: %#v", got)
	}
	if got, _ := two.LookupSymbol(ctx, "OnlyOne"); len(got) != 0 {
		t.Fatalf("cross-tenant symbol visible: %#v", got)
	}
	if stats, _ := one.GetStats(ctx); stats.TotalFiles != 1 || stats.TotalSymbols != 1 {
		t.Fatalf("tenant one stats = %#v", stats)
	}
}

func TestPostgresSymbolStoreMigratesGOB(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, config.ConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	gobPath := config.GetSymbolIndexPath(root)
	gobStore := NewGOBSymbolStore(gobPath)
	if err := gobStore.SaveFileWithSignature(ctx, "legacy.go", "legacy-hash", "legacy-extractor", []Symbol{{Name: "Legacy", Kind: KindFunction, File: "legacy.go", Line: 1}}, []Reference{{SymbolName: "Target", Kind: RefKindCall, File: "legacy.go", Line: 2, CallerName: "Legacy", CallerFile: "legacy.go", CallerLine: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := gobStore.Persist(ctx); err != nil {
		t.Fatal(err)
	}

	store := newIntegrationSymbolStore(t, "migration", root)
	truncateSymbolTables(t, store)
	if err := store.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.LookupSymbol(ctx, "Legacy"); len(got) != 1 {
		t.Fatalf("migrated symbols = %#v", got)
	}
	if got, _ := store.LookupCallers(ctx, "Target"); len(got) != 1 {
		t.Fatalf("migrated refs = %#v", got)
	}
	if _, err := os.Stat(gobPath + ".migrated.bak"); err != nil {
		t.Fatalf("migration backup missing: %v", err)
	}
	if _, err := os.Stat(gobPath); !os.IsNotExist(err) {
		t.Fatalf("original GOB file still exists: %v", err)
	}
}

func TestPostgresMigrationPreservesReferenceOrdinals(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, config.ConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	path := config.GetSymbolIndexPath(root)
	gob := NewGOBSymbolStore(path)
	refs := []Reference{
		{SymbolName: "readFirst", Kind: RefKindRead, File: "main.go", Line: 10, CallerName: "Main"},
		{SymbolName: "calledSecond", Kind: RefKindCall, File: "main.go", Line: 10, CallerName: "Main"},
		{SymbolName: "calledLater", Kind: RefKindCall, File: "main.go", Line: 11, CallerName: "Main"},
	}
	if err := gob.SaveFile(ctx, "main.go", []Symbol{{Name: "Main", File: "main.go", Line: 1}}, refs); err != nil {
		t.Fatal(err)
	}
	want, err := gob.LookupCallees(ctx, "Main", "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.Persist(ctx); err != nil {
		t.Fatal(err)
	}
	pg := newIntegrationSymbolStore(t, "migration-ordinals", root)
	truncateSymbolTables(t, pg)
	if err := pg.Load(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := pg.LookupCallees(ctx, "Main", "main.go")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated ordinal parity = %#v, %v; want %#v", got, err, want)
	}
}

func writeMigrationGOB(t *testing.T, root string, files int) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, config.ConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	path := config.GetSymbolIndexPath(root)
	store := NewGOBSymbolStore(path)
	for i := 0; i < files; i++ {
		file := filepath.ToSlash(filepath.Join("legacy", fmt.Sprintf("%04d.go", i)))
		name := fmt.Sprintf("Legacy%d", i)
		if err := store.SaveFile(context.Background(), file, []Symbol{{Name: name, File: file, Line: 1}}, []Reference{{SymbolName: "Target", Kind: RefKindCall, File: file, Line: 2, CallerName: name, CallerFile: file, CallerLine: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Persist(context.Background()); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPostgresMigrationRollbackAndRetry(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, migrationBatchSize+1)
	store := newIntegrationSymbolStore(t, "migration-retry", root)
	truncateSymbolTables(t, store)
	store.migrationBatchHook = func(batch int) error {
		if batch == 0 {
			return errors.New("injected failure")
		}
		return nil
	}
	if err := store.Load(ctx); err == nil {
		t.Fatal("expected injected migration failure")
	}
	var rows int
	if err := store.pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM symbols WHERE project_id=$1)+(SELECT COUNT(*) FROM refs WHERE project_id=$1)+(SELECT COUNT(*) FROM call_edges WHERE project_id=$1)+(SELECT COUNT(*) FROM symbol_files WHERE project_id=$1)`, identityBytes(store.projectID)).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("failed migration committed %d rows: %v", rows, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("source GOB missing after rollback: %v", err)
	}
	store.migrationBatchHook = nil
	if err := store.Load(ctx); err != nil {
		t.Fatalf("migration retry failed: %v", err)
	}
	if stats, err := store.GetStats(ctx); err != nil || stats.TotalFiles != migrationBatchSize+1 {
		t.Fatalf("retry stats = %#v, %v", stats, err)
	}
}

func TestPostgresMigrationCancellationRollsBack(t *testing.T) {
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	store := newIntegrationSymbolStore(t, "migration-cancel", root)
	truncateSymbolTables(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	store.migrationBatchHook = func(int) error {
		cancel()
		return nil
	}
	if err := store.Load(ctx); err == nil {
		t.Fatal("expected canceled migration to fail")
	}
	var rows int
	if err := store.pool.QueryRow(context.Background(), `SELECT (SELECT COUNT(*) FROM symbols WHERE project_id=$1)+(SELECT COUNT(*) FROM refs WHERE project_id=$1)+(SELECT COUNT(*) FROM call_edges WHERE project_id=$1)+(SELECT COUNT(*) FROM symbol_files WHERE project_id=$1)`, identityBytes(store.projectID)).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("canceled migration committed %d rows: %v", rows, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("source GOB missing after cancellation: %v", err)
	}
}

func TestPostgresMigrationConcurrentLoadsSerialize(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeMigrationGOB(t, root, 2)
	one := newIntegrationSymbolStore(t, "migration-concurrent", root)
	two := newIntegrationSymbolStore(t, "migration-concurrent", root)
	truncateSymbolTables(t, one)
	var batches atomic.Int32
	one.migrationBatchHook = func(int) error { batches.Add(1); return nil }
	two.migrationBatchHook = func(int) error { batches.Add(1); return nil }
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, store := range []*PostgresSymbolStore{one, two} {
		wg.Add(1)
		go func(store *PostgresSymbolStore) { defer wg.Done(); errs <- store.Load(ctx) }(store)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if batches.Load() != 1 {
		t.Fatalf("expected exactly one import, got %d batches", batches.Load())
	}
	if stats, err := one.GetStats(ctx); err != nil || stats.TotalFiles != 2 || stats.TotalSymbols != 2 || stats.TotalReferences != 2 {
		t.Fatalf("concurrent migration data = %#v, %v", stats, err)
	}
}

func TestPostgresMigrationCompletedMarkerArchivesResidualGOB(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	store := newIntegrationSymbolStore(t, "migration-archive-recovery", root)
	truncateSymbolTables(t, store)
	if err := store.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".migrated.bak", path); err != nil {
		t.Fatal(err)
	}
	if err := store.Load(ctx); err != nil {
		t.Fatalf("archive recovery failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("residual GOB was not archived: %v", err)
	}
}

func TestPostgresMigrationFingerprintRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	want, err := fingerprintSource(path)
	if err != nil {
		t.Fatal(err)
	}
	store := newIntegrationSymbolStore(t, "migration-fingerprint", root)
	truncateSymbolTables(t, store)
	if err := store.Load(ctx); err != nil {
		t.Fatal(err)
	}
	var digest []byte
	var size int64
	if err := store.pool.QueryRow(ctx, `SELECT source_digest,source_size FROM symbol_migrations WHERE project_id=$1`, identityBytes(store.projectID)).Scan(&digest, &size); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(digest, want.digest) || size != want.size {
		t.Fatalf("migration fingerprint = %x/%d, want %x/%d", digest, size, want.digest, want.size)
	}
}

func TestPostgresMigrationArchiveFailureKeepsCommittedData(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	backup := path + ".migrated.bak"
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "block"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newIntegrationSymbolStore(t, "migration-archive-failure", root)
	truncateSymbolTables(t, store)
	err := store.Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "migration completed") {
		t.Fatalf("expected actionable archive error, got %v", err)
	}
	if stats, statsErr := store.GetStats(ctx); statsErr != nil || stats.TotalSymbols != 1 {
		t.Fatalf("archive failure removed committed data: %#v, %v", stats, statsErr)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive failure removed source GOB: %v", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		t.Fatal(err)
	}
	if err := store.Load(ctx); err != nil {
		t.Fatalf("archive retry failed: %v", err)
	}
}

func TestPostgresMigrationModifiedResidualFailsClosed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	backup := path + ".migrated.bak"
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "block"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newIntegrationSymbolStore(t, "migration-modified-residual", root)
	truncateSymbolTables(t, store)
	if err := store.Load(ctx); err == nil {
		t.Fatal("expected initial archive failure")
	}
	if err := os.WriteFile(path, append(mustReadFile(t, path), byte('x')), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(backup); err != nil {
		t.Fatal(err)
	}
	err := store.Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("expected source fingerprint mismatch, got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("modified residual GOB was touched: %v", err)
	}
	if stats, err := store.GetStats(ctx); err != nil || stats.TotalSymbols != 1 {
		t.Fatalf("fingerprint mismatch touched Postgres data: %#v, %v", stats, err)
	}
}

func TestPostgresEmptyActivationRejectsLaterGOB(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := newIntegrationSymbolStore(t, "migration-empty-then-gob", root)
	truncateSymbolTables(t, store)
	if err := store.Load(ctx); err != nil {
		t.Fatal(err)
	}
	path := writeMigrationGOB(t, root, 1)
	err := store.Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "no source fingerprint") {
		t.Fatalf("expected unimported-source rejection, got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("new GOB was touched: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPostgresMigrationRejectsPartialRowsWithoutMarker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := writeMigrationGOB(t, root, 1)
	store := newIntegrationSymbolStore(t, "migration-partial", root)
	truncateSymbolTables(t, store)
	if err := store.SaveFile(ctx, "partial.go", []Symbol{{Name: "Partial", File: "partial.go", Line: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	err := store.Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "inconsistent partial") {
		t.Fatalf("expected explicit partial-state error, got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("partial-state guard touched GOB: %v", err)
	}
}

func TestPostgresFileMutationSaveSaveSerializes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	one := newIntegrationSymbolStore(t, "mutation-save-save", root)
	two := newIntegrationSymbolStore(t, "mutation-save-save", root)
	truncateSymbolTables(t, one)
	locked, release := make(chan struct{}), make(chan struct{})
	secondAttempt, secondLocked := make(chan struct{}), make(chan struct{})
	one.mutationHook = func(operation, file string) error {
		close(locked)
		<-release
		return nil
	}
	two.mutationHook = func(operation, file string) error { close(secondLocked); return nil }
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- one.SaveFileWithSignature(ctx, "same.go", "hash-1", "version-1", []Symbol{{Name: "Generation1", File: "same.go", Line: 1}}, []Reference{{SymbolName: "Target1", Kind: RefKindCall, File: "same.go", Line: 2, CallerName: "Generation1"}})
	}()
	<-locked
	secondResult := make(chan error, 1)
	go func() {
		secondAttempt <- struct{}{}
		secondResult <- two.SaveFileWithSignature(ctx, "same.go", "hash-2", "version-2", []Symbol{{Name: "Generation2", File: "same.go", Line: 1}}, []Reference{{SymbolName: "Target2", Kind: RefKindCall, File: "same.go", Line: 2, CallerName: "Generation2"}})
	}()
	<-secondAttempt
	select {
	case <-secondLocked:
		t.Fatal("second save acquired file lock before first released")
	default:
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
	assertSavedGeneration(t, two, "hash-2", "version-2", "Generation2", "Target2")
	if stale, _ := two.LookupCallers(ctx, "Target1"); len(stale) != 0 {
		t.Fatalf("first generation refs remain: %#v", stale)
	}
}

func TestPostgresFileMutationSaveDeleteSerializes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	one := newIntegrationSymbolStore(t, "mutation-save-delete", root)
	two := newIntegrationSymbolStore(t, "mutation-save-delete", root)
	truncateSymbolTables(t, one)
	locked, release := make(chan struct{}), make(chan struct{})
	deleteAttempt, deleteLocked := make(chan struct{}), make(chan struct{})
	one.mutationHook = func(operation, file string) error { close(locked); <-release; return nil }
	two.mutationHook = func(operation, file string) error { close(deleteLocked); return nil }
	saveResult := make(chan error, 1)
	go func() {
		saveResult <- one.SaveFileWithSignature(ctx, "same.go", "hash", "version", []Symbol{{Name: "Saved", File: "same.go", Line: 1}}, []Reference{{SymbolName: "Target", Kind: RefKindCall, File: "same.go", Line: 2, CallerName: "Saved"}})
	}()
	<-locked
	deleteResult := make(chan error, 1)
	go func() { deleteAttempt <- struct{}{}; deleteResult <- two.DeleteFile(ctx, "same.go") }()
	<-deleteAttempt
	select {
	case <-deleteLocked:
		t.Fatal("delete acquired file lock before save released")
	default:
	}
	close(release)
	if err := <-saveResult; err != nil {
		t.Fatal(err)
	}
	if err := <-deleteResult; err != nil {
		t.Fatal(err)
	}
	stats, err := two.GetStats(ctx)
	if err != nil || stats.TotalFiles != 0 || stats.TotalSymbols != 0 || stats.TotalReferences != 0 {
		t.Fatalf("save/delete left partial rows: %#v, %v", stats, err)
	}
}

func assertSavedGeneration(t *testing.T, store *PostgresSymbolStore, hash, version, symbolName, target string) {
	t.Helper()
	if got, ok := store.GetFileContentHash("same.go"); !ok || got != hash {
		t.Fatalf("content hash = %q, %v", got, ok)
	}
	if got, ok := store.GetFileExtractorVersion("same.go"); !ok || got != version {
		t.Fatalf("extractor version = %q, %v", got, ok)
	}
	syms, _ := store.GetSymbolsForFile(context.Background(), "same.go")
	refs, _ := store.LookupCallers(context.Background(), target)
	if len(syms) != 1 || syms[0].Name != symbolName || len(refs) != 1 || refs[0].CallerName != symbolName {
		t.Fatalf("mixed saved generation: symbols=%#v refs=%#v", syms, refs)
	}
}
