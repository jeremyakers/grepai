package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/store"
	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

func TestHandleFileEventDirectoryReplacedByRegularFile(t *testing.T) {
	for _, eventType := range []watcher.EventType{
		watcher.EventDelete,
		watcher.EventRename,
		watcher.EventCreate,
		watcher.EventModify,
	} {
		t.Run(eventType.String(), func(t *testing.T) {
			ctx := context.Background()
			h := newAtomicWriteHarness(t)
			replacementPath := "target.go"
			childPath := filepath.Join(replacementPath, "old.go")
			siblingPath := "target.go-old.go"

			indexDirectoryHarnessFile(t, h, childPath, "package old\nfunc OldChild() {}\n")
			indexDirectoryHarnessFile(t, h, siblingPath, "package sibling\nfunc Sibling() {}\n")
			if err := h.vecStore.SaveDocument(ctx, store.Document{Path: replacementPath, Hash: "stale"}); err != nil {
				t.Fatal(err)
			}
			if err := h.symbolStore.SaveFileWithSignature(ctx, replacementPath, "stale", "test", []trace.Symbol{{Name: "OldExact", File: replacementPath}}, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(h.projectRoot, replacementPath)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.projectRoot, replacementPath), []byte("package replacement\nfunc Replacement() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			h.dispatch(ctx, watcher.FileEvent{Type: eventType, Path: replacementPath, IsDir: true})

			if doc, err := h.vecStore.GetDocument(ctx, childPath); err != nil || doc != nil {
				t.Fatalf("old child document = %#v, err=%v", doc, err)
			}
			if symbols, err := h.symbolStore.LookupSymbol(ctx, "OldChild"); err != nil || len(symbols) != 0 {
				t.Fatalf("old child symbols = %#v, err=%v", symbols, err)
			}
			if doc, err := h.vecStore.GetDocument(ctx, replacementPath); err != nil || doc == nil {
				t.Fatalf("replacement document = %#v, err=%v", doc, err)
			}
			if symbols, err := h.symbolStore.LookupSymbol(ctx, "Replacement"); err != nil || len(symbols) == 0 {
				t.Fatalf("replacement symbols = %#v, err=%v", symbols, err)
			}
			if symbols, err := h.symbolStore.LookupSymbol(ctx, "OldExact"); err != nil || len(symbols) != 0 {
				t.Fatalf("stale exact-path symbols = %#v, err=%v", symbols, err)
			}
			if doc, err := h.vecStore.GetDocument(ctx, siblingPath); err != nil || doc == nil {
				t.Fatalf("same-prefix sibling document = %#v, err=%v", doc, err)
			}
			info, err := os.Lstat(filepath.Join(h.projectRoot, replacementPath))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("replacement on disk: info=%#v err=%v", info, err)
			}
		})
	}
}

func TestHandleFileEventNestedDirectoryUnderRegularAncestor(t *testing.T) {
	for _, eventType := range []watcher.EventType{watcher.EventDelete, watcher.EventRename} {
		t.Run(eventType.String(), func(t *testing.T) {
			ctx := context.Background()
			h := newAtomicWriteHarness(t)
			childPath := filepath.Join("src", "nested", "old.go")
			siblingPath := filepath.Join("src-old", "kept.go")
			indexDirectoryHarnessFile(t, h, childPath, "package nested\nfunc OldNested() {}\n")
			indexDirectoryHarnessFile(t, h, siblingPath, "package old\nfunc Kept() {}\n")

			if err := os.RemoveAll(filepath.Join(h.projectRoot, "src")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.projectRoot, "src"), []byte("replacement"), 0o644); err != nil {
				t.Fatal(err)
			}
			h.dispatch(ctx, watcher.FileEvent{Type: eventType, Path: filepath.Join("src", "nested"), IsDir: true})

			if doc, err := h.vecStore.GetDocument(ctx, childPath); err != nil || doc != nil {
				t.Fatalf("old nested document = %#v, err=%v", doc, err)
			}
			if symbols, err := h.symbolStore.LookupSymbol(ctx, "OldNested"); err != nil || len(symbols) != 0 {
				t.Fatalf("old nested symbols = %#v, err=%v", symbols, err)
			}
			if doc, err := h.vecStore.GetDocument(ctx, siblingPath); err != nil || doc == nil {
				t.Fatalf("same-prefix sibling document = %#v, err=%v", doc, err)
			}
		})
	}
}

func TestReconcileDeletedDirectoryRejectsUnavailableProjectRoot(t *testing.T) {
	for _, setup := range []struct {
		name string
		make func(string) error
	}{
		{name: "missing", make: func(string) error { return nil }},
		{name: "regular", make: func(path string) error { return os.WriteFile(path, []byte("replacement"), 0o644) }},
	} {
		t.Run(setup.name, func(t *testing.T) {
			projectRoot := filepath.Join(t.TempDir(), "project")
			if err := setup.make(projectRoot); err != nil {
				t.Fatal(err)
			}
			var got []directoryAction
			err := reconcileDeletedDirectory(context.Background(), projectRoot, "src", &mockVectorStore{listDocumentsResult: []string{filepath.Join("src", "old.go")}}, nil, func(action directoryAction) {
				got = append(got, action)
			})
			if err == nil || len(got) != 0 {
				t.Fatalf("events=%#v err=%v, want no mutation and an error", got, err)
			}
		})
	}
}

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

func TestHandleFileEventDirectoryReplacementSkipsUnsupportedFiles(t *testing.T) {
	for _, replacementPath := range []string{"target.xyz", "target"} {
		t.Run(replacementPath, func(t *testing.T) {
			ctx := context.Background()
			h := newAtomicWriteHarness(t)
			childPath := filepath.Join(replacementPath, "old.go")
			indexDirectoryHarnessFile(t, h, childPath, "package old\nfunc Old() {}\n")

			if err := os.RemoveAll(filepath.Join(h.projectRoot, replacementPath)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.projectRoot, replacementPath), []byte("unsupported replacement"), 0o644); err != nil {
				t.Fatal(err)
			}
			h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: replacementPath, IsDir: true})

			if doc, err := h.vecStore.GetDocument(ctx, childPath); err != nil || doc != nil {
				t.Fatalf("old child document = %#v, err=%v", doc, err)
			}
			if doc, err := h.vecStore.GetDocument(ctx, replacementPath); err != nil || doc != nil {
				t.Fatalf("unsupported replacement document = %#v, err=%v", doc, err)
			}
			if info, err := os.Lstat(filepath.Join(h.projectRoot, replacementPath)); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("replacement on disk: info=%#v err=%v", info, err)
			}
		})
	}
}

func TestHandleFileEventDirectoryReplacementHonorsCustomExtension(t *testing.T) {
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	h.scanner.WithCustomExtensions([]string{".xyz"})
	replacementPath := "target.xyz"
	childPath := filepath.Join(replacementPath, "old.go")
	indexDirectoryHarnessFile(t, h, childPath, "package old\nfunc Old() {}\n")
	if err := os.RemoveAll(filepath.Join(h.projectRoot, replacementPath)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.projectRoot, replacementPath), []byte("custom replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: replacementPath, IsDir: true})

	if doc, err := h.vecStore.GetDocument(ctx, childPath); err != nil || doc != nil {
		t.Fatalf("old child document = %#v, err=%v", doc, err)
	}
	if doc, err := h.vecStore.GetDocument(ctx, replacementPath); err != nil || doc == nil {
		t.Fatalf("custom replacement document = %#v, err=%v", doc, err)
	}
}

func TestHandleFileEventDirectoryReplacementHonorsIgnorePolicy(t *testing.T) {
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	replacementPath := "target.go"
	childPath := filepath.Join(replacementPath, "keep.go")
	indexDirectoryHarnessFile(t, h, childPath, "package keep\nfunc Keep() {}\n")
	if err := os.WriteFile(filepath.Join(h.projectRoot, ".grepaiignore"), []byte("target.go\n!target.go/keep.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignore, err := indexer.NewIgnoreMatcher(h.projectRoot, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ignore.ShouldIgnore(replacementPath) || ignore.ShouldIgnore(childPath) {
		t.Fatal("invalid ignore-policy test setup")
	}
	h.scanner = indexer.NewScanner(h.projectRoot, ignore)

	if err := os.RemoveAll(filepath.Join(h.projectRoot, replacementPath)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.projectRoot, replacementPath), []byte("package ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: replacementPath, IsDir: true})

	if doc, err := h.vecStore.GetDocument(ctx, childPath); err != nil || doc != nil {
		t.Fatalf("old re-included child document = %#v, err=%v", doc, err)
	}
	if doc, err := h.vecStore.GetDocument(ctx, replacementPath); err != nil || doc != nil {
		t.Fatalf("ignored replacement document = %#v, err=%v", doc, err)
	}
	if info, err := os.Lstat(filepath.Join(h.projectRoot, replacementPath)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("replacement on disk: info=%#v err=%v", info, err)
	}
}
