package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/store"
)

func TestRemainingRecoveryCASConflictPublishesLatestVerification(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := t.TempDir()
	relative := filepath.Join("linked", "a.go")
	absolute := filepath.Join(target, "a.go")
	oldContent := "package old\n"
	newContent := "package newer\n"
	if err := os.WriteFile(absolute, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	ignore, err := NewIgnoreMatcher(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(root, ignore)
	initial, err := scanner.ScanFile(relative)
	if err != nil || initial == nil {
		t.Fatalf("initial=%v err=%v", initial, err)
	}
	st := &conflictRefreshStore{mockStore: newMockStore(), path: absolute, newContent: newContent, mode: "matching"}
	st.documents[relative] = store.Document{Path: relative, Hash: initial.Hash, ChunkIDs: []string{"old-chunk"}}
	embedder := newMockEmbedder()
	idx := NewIndexer(root, st, embedder, NewChunker(512, 50), scanner, time.Now())
	stats, err := idx.IndexAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := scanner.ScanFile(relative)
	if err != nil || latest == nil {
		t.Fatalf("latest=%v err=%v", latest, err)
	}
	verified, ok := stats.VerifiedUnchangedFiles[relative]
	if !ok {
		t.Fatalf("verified=%v", stats.VerifiedUnchangedFiles)
	}
	if verified.Hash != latest.Hash || verified.Size != latest.Size || !verified.ModTime.Equal(latest.ObservedModTime) {
		t.Fatalf("verified=%+v latest=%+v", verified, latest)
	}
	if len(stats.ScannedFiles) != 1 || stats.ScannedFiles[0].Path != relative || stats.ScannedFiles[0].Size != latest.Size || !stats.ScannedFiles[0].ObservedModTime.Equal(latest.ObservedModTime) {
		t.Fatalf("scanned=%v latest=%+v", stats.ScannedFiles, latest)
	}
	if stats.FilesIndexed != 0 || embedder.embedCalled {
		t.Fatalf("indexed=%d embedCalled=%v", stats.FilesIndexed, embedder.embedCalled)
	}
	doc, err := st.GetDocument(ctx, relative)
	if err != nil || doc == nil || doc.Hash != latest.Hash {
		t.Fatalf("document=%+v err=%v", doc, err)
	}
}
