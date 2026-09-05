package trace

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
)

func TestResolveSymbolPostgresDSNOrder(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Store.Postgres.DSN = "project"
	workspaceStore := &config.StoreConfig{Postgres: config.PostgresConfig{DSN: "workspace"}}

	tests := []struct {
		name       string
		traceDSN   string
		workspace  *config.StoreConfig
		projectDSN string
		wantDSN    string
		wantSource string
	}{
		{"trace wins", "trace", workspaceStore, "project", "trace", "trace.postgres.dsn"},
		{"workspace precedes project", "", workspaceStore, "project", "workspace", "workspace store.postgres.dsn"},
		{"project fallback", "", nil, "project", "project", "project store.postgres.dsn"},
		{"default fallback", "", nil, "", config.DefaultPostgresDSN, "default Postgres DSN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.Trace.Postgres.DSN = tt.traceDSN
			cfg.Store.Postgres.DSN = tt.projectDSN
			got, source := resolveSymbolPostgresDSN(cfg, tt.workspace)
			if got != tt.wantDSN || source != tt.wantSource {
				t.Fatalf("got (%q, %q), want (%q, %q)", got, source, tt.wantDSN, tt.wantSource)
			}
		})
	}
}

func TestNewSymbolStoreDefaultsToGOB(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Trace.StoreBackend = ""
	store, err := NewSymbolStore(context.Background(), cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.(*GOBSymbolStore); !ok {
		t.Fatalf("got %T, want *GOBSymbolStore", store)
	}
}

func TestNewSymbolStoreRejectsUnknownBackend(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Trace.StoreBackend = "unknown"
	if _, err := NewSymbolStore(context.Background(), cfg, t.TempDir()); err == nil {
		t.Fatal("expected unknown backend error")
	}
}

func TestSymbolStoreBackendSelection(t *testing.T) {
	for input, want := range map[string]string{"": "gob", "gob": "gob", "postgres": "postgres"} {
		got, err := symbolStoreBackend(input)
		if err != nil || got != want {
			t.Fatalf("symbolStoreBackend(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

func TestSymbolSchemaQueriesAreIdempotent(t *testing.T) {
	queries := symbolSchemaQueries()
	if second := symbolSchemaQueries(); !reflect.DeepEqual(queries, second) {
		t.Fatal("schema generation is not deterministic")
	}
	seen := make(map[string]bool, len(queries))
	for _, query := range queries {
		if query == "" {
			t.Fatal("schema query must not be empty")
		}
		if seen[query] {
			t.Fatalf("duplicate schema query: %s", query)
		}
		seen[query] = true
		if query[:6] == "CREATE" && !containsIFNotExists(query) {
			t.Fatalf("CREATE query is not idempotent: %s", query)
		}
	}
}

func containsIFNotExists(query string) bool {
	for i := 0; i+13 <= len(query); i++ {
		if query[i:i+13] == "IF NOT EXISTS" {
			return true
		}
	}
	return false
}

func TestMigrationAdvisoryKeyIsStableAndProjectScoped(t *testing.T) {
	a1, a2 := migrationAdvisoryKey("project-a")
	a1Again, a2Again := migrationAdvisoryKey("project-a")
	b1, b2 := migrationAdvisoryKey("project-b")
	if a1 != a1Again || a2 != a2Again {
		t.Fatal("migration advisory key is not stable")
	}
	if a1 == b1 && a2 == b2 {
		t.Fatal("different projects must not share an advisory key")
	}
	f1, f2 := fileMutationAdvisoryKey("project-a", "file.go")
	if a1 == f1 && a2 == f2 {
		t.Fatal("migration and file mutation keys must use distinct namespaces")
	}
	if other1, other2 := fileMutationAdvisoryKey("project-a", "other.go"); f1 == other1 && f2 == other2 {
		t.Fatal("different files must not share a mutation key")
	}
}

func TestSymbolSchemaUsesLosslessIdentityColumnsAndMigrationState(t *testing.T) {
	schema := strings.Join(symbolSchemaQueries(), "\n")
	for _, required := range []string{
		"symbol_files (project_id BYTEA", "path BYTEA",
		"symbols (project_id BYTEA", "name BYTEA", "file BYTEA",
		"refs (project_id BYTEA", "symbol_name BYTEA", "caller BYTEA", "caller_file BYTEA",
		"call_edges (project_id BYTEA", "callee BYTEA",
		"symbol_migrations (project_id BYTEA PRIMARY KEY", "source_digest BYTEA", "source_size BIGINT", "completed_at TIMESTAMPTZ",
		"refs ADD COLUMN IF NOT EXISTS ordinal", "call_edges ADD COLUMN IF NOT EXISTS ordinal",
	} {
		if !strings.Contains(schema, required) {
			t.Fatalf("symbol schema missing %q", required)
		}
	}
}

func TestCalleeQueriesUseDurableOrdinals(t *testing.T) {
	for _, query := range []string{calleeEdgesSQL, calleeRefsSQL} {
		if strings.Contains(strings.ToLower(query), "ctid") {
			t.Fatalf("callee query depends on ctid: %s", query)
		}
		if !strings.Contains(query, "ORDER BY file,line,ordinal") {
			t.Fatalf("callee query does not use durable ordinal ordering: %s", query)
		}
	}
}

func TestMigrationRefsByFileReconstructsOriginalOrder(t *testing.T) {
	store := NewGOBSymbolStore("unused")
	refs := []Reference{
		{SymbolName: "readFirst", Kind: RefKindRead, File: "main.go", Line: 10, CallerName: "Main"},
		{SymbolName: "callSecond", Kind: RefKindCall, File: "main.go", Line: 10, CallerName: "Main"},
		{SymbolName: "writeThird", Kind: RefKindWrite, File: "main.go", Line: 11, CallerName: "Main"},
	}
	if err := store.SaveFile(context.Background(), "main.go", nil, refs); err != nil {
		t.Fatal(err)
	}
	got := migrationRefsByFile(store)["main.go"]
	if !reflect.DeepEqual(got, refs) {
		t.Fatalf("migration ref order = %#v, want %#v", got, refs)
	}
}
