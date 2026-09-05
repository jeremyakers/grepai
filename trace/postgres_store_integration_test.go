package trace

import (
	"context"
	"os"
	"path/filepath"
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
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE TABLE symbols, refs, call_edges, symbol_files`); err != nil {
		t.Fatalf("failed to truncate symbol tables: %v", err)
	}
}

func TestPostgresSymbolStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newIntegrationSymbolStore(t, "round-trip", t.TempDir())
	truncateSymbolTables(t, store)

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
