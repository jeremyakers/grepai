package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/trace"
)

func TestRemoveOfflineSymbolFilesRemovesOnlyConfirmedOrphans(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "kept.go"), []byte("package kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignore, err := indexer.NewIgnoreMatcher(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	scanner := indexer.NewScanner(root, ignore)
	symbols := trace.NewGOBSymbolStore(filepath.Join(t.TempDir(), "symbols.gob"))
	for _, path := range []string{"kept.go", "deleted.go"} {
		if err := symbols.SaveFileWithSignature(ctx, path, "hash", "version", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := symbols.ListFileFingerprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeOfflineSymbolFiles(ctx, scanner, symbols, snapshot, []indexer.FileMeta{{Path: "kept.go"}}); err != nil {
		t.Fatal(err)
	}
	if !symbols.IsFileIndexed("kept.go") {
		t.Fatal("existing file was removed")
	}
	if symbols.IsFileIndexed("deleted.go") {
		t.Fatal("confirmed offline orphan was retained")
	}
}

func TestRemoveOfflineSymbolFilesPreservesSnapshotWhenRootIsMissing(t *testing.T) {
	root := t.TempDir()
	ignore, err := indexer.NewIgnoreMatcher(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	scanner := indexer.NewScanner(root, ignore)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	symbols := trace.NewGOBSymbolStore(filepath.Join(t.TempDir(), "symbols.gob"))
	if err := symbols.SaveFile(context.Background(), "old.go", nil, nil); err != nil {
		t.Fatal(err)
	}
	err = removeOfflineSymbolFiles(context.Background(), scanner, symbols, map[string]trace.FileFingerprint{"old.go": {}}, nil)
	if err == nil || !symbols.IsFileIndexed("old.go") {
		t.Fatalf("err=%v indexed=%v", err, symbols.IsFileIndexed("old.go"))
	}
}
