package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

type legacyIndexedFileStore struct {
	trace.SymbolStore
	callEdgesCalls int
}

func (s *legacyIndexedFileStore) GetCallEdges(ctx context.Context) ([]trace.CallEdge, error) {
	s.callEdgesCalls++
	return s.SymbolStore.GetCallEdges(ctx)
}

func TestIndexedPathsUnderDirectoryUsesConservativeLegacyFallback(t *testing.T) {
	ctx := context.Background()
	vector := &mockVectorStore{listDocumentsResult: []string{"src/vector.go", "src-old/keep.go"}}
	underlying := trace.NewGOBSymbolStore(filepath.Join(t.TempDir(), "symbols.gob"))
	if err := underlying.SaveFileWithSignature(ctx, "src/symbol.go", "hash", "version", nil, nil); err != nil {
		t.Fatal(err)
	}
	legacy := &legacyIndexedFileStore{SymbolStore: underlying}
	got, err := indexedPathsUnderDirectory(ctx, "src", vector, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "src/vector.go" {
		t.Fatalf("paths = %v", got)
	}
	if legacy.callEdgesCalls != 0 {
		t.Fatalf("GetCallEdges calls = %d, want 0", legacy.callEdgesCalls)
	}
}

func TestPlanDeletedDirectoryPreservesAllPathsOnStatFailure(t *testing.T) {
	for _, cause := range []error{syscall.EACCES, syscall.EIO} {
		t.Run(cause.Error(), func(t *testing.T) {
			calls := 0
			events, err := planDeletedDirectory(context.Background(), "/project", []string{"src/absent.go", "src/unreadable.go"}, func(path string) (fs.FileInfo, error) {
				calls++
				if filepath.Base(path) == "absent.go" {
					return nil, fs.ErrNotExist
				}
				return nil, cause
			})
			if !errors.Is(err, cause) || events != nil || calls != 2 {
				t.Fatalf("events=%#v err=%v calls=%d", events, err, calls)
			}
		})
	}
}

func TestRequalifyRemovedFilePreservesOnStatFailure(t *testing.T) {
	for _, cause := range []error{syscall.EACCES, syscall.EIO} {
		t.Run(cause.Error(), func(t *testing.T) {
			event := watcher.FileEvent{Type: watcher.EventRename, Path: "main.go"}
			eventType, err := requalifyRemovedFile("/project", event, func(string) (fs.FileInfo, error) {
				return nil, cause
			})
			if eventType != watcher.EventRename || !errors.Is(err, cause) {
				t.Fatalf("type=%v err=%v", eventType, err)
			}
		})
	}
}

func TestHandleFileEventDirectoryDeleteReconcilesExactDescendants(t *testing.T) {
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	indexDirectoryHarnessFile(t, h, filepath.Join("src", "nested", "indexed.go"), "package nested\nfunc Indexed() {}\n")
	indexDirectoryHarnessFile(t, h, filepath.Join("src-old", "kept.go"), "package old\nfunc Kept() {}\n")
	if err := h.symbolStore.SaveFileWithSignature(ctx, filepath.Join("src", "symbols.go"), "hash", "test", []trace.Symbol{{Name: "SymbolOnly", File: filepath.Join("src", "symbols.go")}}, nil); err != nil {
		t.Fatal(err)
	}
	zeroSymbolPath := filepath.Join("src", "zero.go")
	if err := h.symbolStore.SaveFileWithSignature(ctx, zeroSymbolPath, "hash", "test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(h.projectRoot, "src")); err != nil {
		t.Fatal(err)
	}
	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventDelete, Path: "src", IsDir: true})
	if doc, err := h.vecStore.GetDocument(ctx, filepath.Join("src", "nested", "indexed.go")); err != nil || doc != nil {
		t.Fatalf("deleted document = %#v, err=%v", doc, err)
	}
	if symbols, err := h.symbolStore.LookupSymbol(ctx, "SymbolOnly"); err != nil || len(symbols) != 0 {
		t.Fatalf("symbol-only descendant survived: %#v, err=%v", symbols, err)
	}
	if h.symbolStore.IsFileIndexed(zeroSymbolPath) {
		t.Fatal("zero-symbol descendant survived")
	}
	if doc, err := h.vecStore.GetDocument(ctx, filepath.Join("src-old", "kept.go")); err != nil || doc == nil {
		t.Fatalf("boundary sibling document = %#v, err=%v", doc, err)
	}
	if symbols, err := h.symbolStore.LookupSymbol(ctx, "Kept"); err != nil || len(symbols) == 0 {
		t.Fatalf("boundary sibling symbols removed: %#v, err=%v", symbols, err)
	}
}

func TestHandleFileEventDirectoryDeletePreservesRecreatedFiles(t *testing.T) {
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	oldPath := filepath.Join("src", "old.go")
	stalePath := filepath.Join("src", "stale.go")
	indexDirectoryHarnessFile(t, h, oldPath, "package src\nfunc Old() {}\n")
	indexDirectoryHarnessFile(t, h, stalePath, "package src\nfunc Stale() {}\n")
	if err := os.RemoveAll(filepath.Join(h.projectRoot, "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.projectRoot, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.projectRoot, oldPath), []byte("package src\nfunc Replacement() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: "src", IsDir: true})
	if symbols, err := h.symbolStore.LookupSymbol(ctx, "Replacement"); err != nil || len(symbols) == 0 {
		t.Fatalf("replacement missing: %#v, err=%v", symbols, err)
	}
	if doc, err := h.vecStore.GetDocument(ctx, stalePath); err != nil || doc != nil {
		t.Fatalf("stale document = %#v, err=%v", doc, err)
	}
}

func TestHandleFileEventRemovalPreservesSymlinkReplacement(t *testing.T) {
	ctx := context.Background()
	h := newAtomicWriteHarness(t)
	if err := os.Remove(h.srcPath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(h.projectRoot, "replacement.go")
	if err := os.WriteFile(target, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, h.srcPath); err != nil {
		t.Fatal(err)
	}
	h.dispatch(ctx, watcher.FileEvent{Type: watcher.EventRename, Path: "main.go"})
	if doc, err := h.vecStore.GetDocument(ctx, "main.go"); err != nil || doc == nil {
		t.Fatalf("symlink replacement purged index: %#v, err=%v", doc, err)
	}
}

func indexDirectoryHarnessFile(t *testing.T, h *atomicWriteHarness, path, content string) {
	t.Helper()
	abs := filepath.Join(h.projectRoot, path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	h.dispatch(context.Background(), watcher.FileEvent{Type: watcher.EventCreate, Path: path})
}
