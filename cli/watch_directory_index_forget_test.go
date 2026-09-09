package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/watcher"
)

func TestHandleFileEventRecreatedDirectoryForgetsNowIgnoredIndexedFile(t *testing.T) {
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	path := filepath.Join("src", "indexed.go")
	content := "package src\nfunc Indexed() { Target() }\n"
	indexDirectoryHarnessFile(t, h, path, content)
	if refs, err := h.symbolStore.LookupCallers(ctx, "Target"); err != nil || len(refs) == 0 {
		t.Fatalf("setup references = %#v, err=%v", refs, err)
	}

	if err := os.RemoveAll(filepath.Join(h.projectRoot, "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.projectRoot, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.projectRoot, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.projectRoot, ".grepaiignore"), []byte("src/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignore, err := indexer.NewIgnoreMatcher(h.projectRoot, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	h.scanner = indexer.NewScanner(h.projectRoot, ignore)
	if h.scanner.ShouldIndexPath(path) {
		t.Fatal("invalid ignore-policy test setup")
	}

	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: "src", IsDir: true})

	if doc, err := h.vecStore.GetDocument(ctx, path); err != nil || doc != nil {
		t.Fatalf("ignored document = %#v, err=%v", doc, err)
	}
	if symbols, err := h.symbolStore.LookupSymbol(ctx, "Indexed"); err != nil || len(symbols) != 0 {
		t.Fatalf("ignored symbols = %#v, err=%v", symbols, err)
	}
	if refs, err := h.symbolStore.LookupCallers(ctx, "Target"); err != nil || len(refs) != 0 {
		t.Fatalf("ignored references = %#v, err=%v", refs, err)
	}
	if got, err := os.ReadFile(filepath.Join(h.projectRoot, path)); err != nil || string(got) != content {
		t.Fatalf("physical file changed: content=%q err=%v", got, err)
	}
}

func TestHandleFileEventDirectorySymlinkReplacementForgetsDescendantsOnly(t *testing.T) {
	skipIfWindows(t)
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	childPath := filepath.Join("src", "old.go")
	indexDirectoryHarnessFile(t, h, childPath, "package src\nfunc Old() { External() }\n")
	if refs, err := h.symbolStore.LookupCallers(ctx, "External"); err != nil || len(refs) == 0 {
		t.Fatalf("setup references = %#v, err=%v", refs, err)
	}

	targetDir := t.TempDir()
	targetChild := filepath.Join(targetDir, "old.go")
	targetContent := []byte("package outside\nfunc Outside() {}\n")
	if err := os.WriteFile(targetChild, targetContent, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(targetChild)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(h.projectRoot, "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetDir, filepath.Join(h.projectRoot, "src")); err != nil {
		t.Fatal(err)
	}
	embedCalls, embedBatchCalls := h.emb.embedCalls, h.emb.embedBatchCalls

	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: "src", IsDir: true})

	if doc, err := h.vecStore.GetDocument(ctx, childPath); err != nil || doc != nil {
		t.Fatalf("old descendant document = %#v, err=%v", doc, err)
	}
	if symbols, err := h.symbolStore.LookupSymbol(ctx, "Old"); err != nil || len(symbols) != 0 {
		t.Fatalf("old descendant symbols = %#v, err=%v", symbols, err)
	}
	if refs, err := h.symbolStore.LookupCallers(ctx, "External"); err != nil || len(refs) != 0 {
		t.Fatalf("old descendant references = %#v, err=%v", refs, err)
	}
	if h.emb.embedCalls != embedCalls || h.emb.embedBatchCalls != embedBatchCalls {
		t.Fatalf("replacement target was indexed: embed calls %d/%d -> %d/%d", embedCalls, embedBatchCalls, h.emb.embedCalls, h.emb.embedBatchCalls)
	}
	if link, err := os.Readlink(filepath.Join(h.projectRoot, "src")); err != nil || link != targetDir {
		t.Fatalf("replacement link = %q, err=%v", link, err)
	}
	after, err := os.Stat(targetChild)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(targetChild)
	if err != nil || string(got) != string(targetContent) || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("target modified: content=%q before=%v after=%v err=%v", got, before, after, err)
	}
}

func TestHandleFileEventDirectorySocketReplacementForgetsDescendantsOnly(t *testing.T) {
	skipIfWindows(t)
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	childPath := filepath.Join("src", "old.go")
	indexDirectoryHarnessFile(t, h, childPath, "package src\nfunc OldSocketChild() { SocketTarget() }\n")

	replacementPath := filepath.Join(h.projectRoot, "src")
	if err := os.RemoveAll(replacementPath); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", replacementPath)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	defer listener.Close()

	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: "src", IsDir: true})

	if doc, err := h.vecStore.GetDocument(ctx, childPath); err != nil || doc != nil {
		t.Fatalf("old descendant document = %#v, err=%v", doc, err)
	}
	if symbols, err := h.symbolStore.LookupSymbol(ctx, "OldSocketChild"); err != nil || len(symbols) != 0 {
		t.Fatalf("old descendant symbols = %#v, err=%v", symbols, err)
	}
	if refs, err := h.symbolStore.LookupCallers(ctx, "SocketTarget"); err != nil || len(refs) != 0 {
		t.Fatalf("old descendant references = %#v, err=%v", refs, err)
	}
	if info, err := os.Lstat(replacementPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket replacement changed: info=%#v err=%v", info, err)
	}
}
