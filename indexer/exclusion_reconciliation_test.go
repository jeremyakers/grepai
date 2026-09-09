package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/store"
)

func writeExclusionFixtures(t *testing.T, root string) []string {
	t.Helper()
	files := map[string][]byte{
		"ignored.go":      []byte("package ignored\n"),
		"unsupported.xyz": []byte("unsupported\n"),
		"bundle.min.js":   []byte("const minified = true\n"),
		"binary.go":       {'p', 'a', 'c', 'k', 'a', 'g', 'e', 0, 'x'},
		"unreadable.go":   []byte("package unreadable\n"),
	}
	paths := make([]string, 0, len(files)+1)
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), content, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if err := os.WriteFile(filepath.Join(root, "large.go"), []byte(strings.Repeat("x", maxFileSize+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	return append(paths, "large.go")
}

func TestIndexAllPurgesPolicyExclusionsButPreservesReadErrors(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := writeExclusionFixtures(t, root)
	ignore, err := NewIgnoreMatcher(root, []string{"ignored.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(root, ignore)
	scanner.readSnapshot = func(path, relPath string) (*FileInfo, error) {
		if relPath == "unreadable.go" {
			return nil, errors.New("simulated read failure")
		}
		return readFileSnapshot(path, relPath)
	}
	st := store.NewGOBStore(filepath.Join(root, "index.gob"))
	for _, path := range paths {
		if err := st.SaveDocument(ctx, store.Document{Path: path, Hash: "old", ChunkIDs: []string{"chunk"}}); err != nil {
			t.Fatal(err)
		}
	}
	idx := NewIndexer(root, st, newMockEmbedder(), NewChunker(512, 50), scanner, time.Time{})
	if _, err := idx.IndexAll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"ignored.go", "unsupported.xyz", "bundle.min.js", "large.go", "binary.go"} {
		if doc, err := st.GetDocument(ctx, path); err != nil || doc != nil {
			t.Errorf("excluded %s retained: doc=%v err=%v", path, doc, err)
		}
	}
	if doc, err := st.GetDocument(ctx, "unreadable.go"); err != nil || doc == nil {
		t.Fatalf("read-error document removed: doc=%v err=%v", doc, err)
	}
}
