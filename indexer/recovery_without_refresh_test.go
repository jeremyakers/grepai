package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/store"
)

type noRefreshMutatingStore struct {
	store.VectorStore
	source      store.DocumentMetadataSource
	once        sync.Once
	mutate      func() error
	mutationErr error
}

func (s *noRefreshMutatingStore) ListDocumentMetadata(ctx context.Context) ([]store.DocumentMetadata, error) {
	return s.source.ListDocumentMetadata(ctx)
}

func (s *noRefreshMutatingStore) GetDocument(ctx context.Context, path string) (*store.Document, error) {
	s.once.Do(func() { s.mutationErr = s.mutate() })
	if s.mutationErr != nil {
		return nil, s.mutationErr
	}
	return s.VectorStore.GetDocument(ctx, path)
}

type unsupportedRefreshMutatingStore struct{ *noRefreshMutatingStore }

func (*unsupportedRefreshMutatingStore) RefreshDocumentModTime(context.Context, string, string, time.Time) (bool, error) {
	return false, store.ErrRefreshUnsupported
}

func TestRecoveredDeletedRecordWithoutRefreshCapabilityUsesFreshSource(t *testing.T) {
	ctx, root, _, scanner, base, _ := setupMutatingRecovery(t)
	relative := filepath.Join("linked", "a.go")
	st := &noRefreshMutatingStore{VectorStore: base, source: base}
	st.mutate = func() error {
		if err := base.DeleteByFile(ctx, relative); err != nil {
			return err
		}
		return base.DeleteDocument(ctx, relative)
	}
	embedder := newMockEmbedder()
	stats, err := NewIndexer(root, st, embedder, NewChunker(512, 50), scanner, time.Now()).IndexAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesIndexed != 1 || !embedder.embedCalled {
		t.Fatalf("indexed=%d embedCalled=%v", stats.FilesIndexed, embedder.embedCalled)
	}
	doc, err := base.GetDocument(ctx, relative)
	if err != nil || doc == nil || len(doc.ChunkIDs) == 0 {
		t.Fatalf("recreated document=%+v err=%v", doc, err)
	}
}

func TestRecoveredReplacedRecordWithUnsupportedRefreshUsesLatestState(t *testing.T) {
	ctx, root, absolute, scanner, base, _ := setupMutatingRecovery(t)
	relative := filepath.Join("linked", "a.go")
	hidden := &noRefreshMutatingStore{VectorStore: base, source: base}
	hidden.mutate = func() error {
		if err := os.WriteFile(absolute, []byte("package latest\n"), 0o644); err != nil {
			return err
		}
		latest, err := scanner.ScanFile(relative)
		if err != nil {
			return err
		}
		if err := base.DeleteByFile(ctx, relative); err != nil {
			return err
		}
		if err := base.SaveChunks(ctx, []store.Chunk{{ID: "latest", FilePath: relative}}); err != nil {
			return err
		}
		return base.SaveDocument(ctx, store.Document{Path: relative, Hash: latest.Hash, ModTime: latest.ObservedModTime, HasExactModTime: true, ChunkIDs: []string{"latest"}})
	}
	st := &unsupportedRefreshMutatingStore{noRefreshMutatingStore: hidden}
	embedder := newMockEmbedder()
	stats, err := NewIndexer(root, st, embedder, NewChunker(512, 50), scanner, time.Now()).IndexAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := scanner.ScanFile(relative)
	if err != nil {
		t.Fatal(err)
	}
	verified := stats.VerifiedUnchangedFiles[relative]
	if verified.Hash != latest.Hash || verified.Size != latest.Size || !verified.ModTime.Equal(latest.ObservedModTime) {
		t.Fatalf("verified=%+v latest=%+v", verified, latest)
	}
	if stats.FilesIndexed != 0 || embedder.embedCalled {
		t.Fatalf("indexed=%d embedCalled=%v", stats.FilesIndexed, embedder.embedCalled)
	}
	doc, err := base.GetDocument(ctx, relative)
	if err != nil || doc == nil || doc.Hash != latest.Hash || len(doc.ChunkIDs) != 1 || doc.ChunkIDs[0] != "latest" {
		t.Fatalf("document=%+v err=%v", doc, err)
	}
}
