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
	if err := os.WriteFile(filepath.Join(root, "ignored.go"), []byte("package ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignore, err := indexer.NewIgnoreMatcher(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	scanner := indexer.NewScanner(root, ignore)
	symbols := trace.NewGOBSymbolStore(filepath.Join(t.TempDir(), "symbols.gob"))
	for _, path := range []string{"kept.go", "ignored.go", "deleted.go"} {
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
	if !symbols.IsFileIndexed("ignored.go") {
		t.Fatal("unseen but existing file was removed")
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

func TestRemoveOfflineSymbolFilesAcceptsVerifiedCaseRenameWitness(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignore, err := indexer.NewIgnoreMatcher(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	scanner := indexer.NewScanner(root, ignore)
	symbols := trace.NewGOBSymbolStore(filepath.Join(t.TempDir(), "symbols.gob"))
	if err := symbols.SaveFileWithSignature(ctx, "Foo.go", "hash", "version", nil, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := symbols.ListFileFingerprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finder := func(_ string, candidates []string, scanned []indexer.FileMeta) map[string]string {
		if len(candidates) != 1 || candidates[0] != "Foo.go" || len(scanned) != 1 || scanned[0].Path != "foo.go" {
			t.Fatalf("candidates=%v scanned=%v", candidates, scanned)
		}
		return map[string]string{"Foo.go": "foo.go"}
	}
	if err := removeOfflineSymbolFilesWithCaseRenames(ctx, scanner, symbols, snapshot, []indexer.FileMeta{{Path: "foo.go"}}, finder); err != nil {
		t.Fatal(err)
	}
	if symbols.IsFileIndexed("Foo.go") {
		t.Fatal("old symbol spelling retained after witnessed case rename")
	}
}

func TestOfflineCaseRenameWithAncestorSpellingChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	oldDir := filepath.Join(root, "Dir")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "Foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	temporaryDir := filepath.Join(root, "case-rename-temp")
	newDir := filepath.Join(root, "dir")
	if err := os.Rename(oldDir, temporaryDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryDir, newDir); err != nil {
		t.Fatal(err)
	}
	temporaryFile := filepath.Join(newDir, "case-rename-temp.go")
	if err := os.Rename(filepath.Join(newDir, "Foo.go"), temporaryFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryFile, filepath.Join(newDir, "foo.go")); err != nil {
		t.Fatal(err)
	}
	ignore, err := indexer.NewIgnoreMatcher(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	scanner := indexer.NewScanner(root, ignore)
	scanned, _, err := scanner.ScanMetadata()
	if err != nil {
		t.Fatal(err)
	}
	symbols := trace.NewGOBSymbolStore(filepath.Join(t.TempDir(), "symbols.gob"))
	if err := symbols.SaveFileWithSignature(ctx, "Dir/Foo.go", "hash", "version", nil, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := symbols.ListFileFingerprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeOfflineSymbolFiles(ctx, scanner, symbols, snapshot, scanned); err != nil {
		t.Fatal(err)
	}
	if symbols.IsFileIndexed("Dir/Foo.go") {
		t.Fatal("ancestor case-rename left old symbol spelling")
	}
}
