package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/grepai/store"
)

type completeDocumentStore struct {
	*mockVectorStore
	path string
	doc  *store.Document
	err  error
}

func (s *completeDocumentStore) GetCompleteDocument(_ context.Context, path string) (*store.Document, error) {
	s.path = path
	return s.doc, s.err
}

func TestProjectPrefixStoreForwardsCompleteDocumentWithNativeScope(t *testing.T) {
	root := t.TempDir()
	backendDoc := &store.Document{Path: "workspace/project/nested/a.go", Hash: "hash", ChunkIDs: []string{"c1"}}
	backend := &completeDocumentStore{mockVectorStore: &mockVectorStore{}, doc: backendDoc}
	prefixed := &projectPrefixStore{
		store: backend, workspaceName: "workspace", projectName: "project", projectPath: root,
	}
	got, err := prefixed.GetCompleteDocument(context.Background(), filepath.Join(root, "nested", "a.go"))
	if err != nil || got == nil {
		t.Fatalf("GetCompleteDocument() = %+v, %v", got, err)
	}
	if backend.path != "workspace/project/nested/a.go" {
		t.Fatalf("backend path = %q", backend.path)
	}
	if got.Path != filepath.Join("nested", "a.go") || got.Hash != "hash" || len(got.ChunkIDs) != 1 || got.ChunkIDs[0] != "c1" {
		t.Fatalf("scanner-native document = %+v", got)
	}
	got.Hash = "changed"
	got.ChunkIDs[0] = "changed"
	if backendDoc.Hash != "hash" || backendDoc.ChunkIDs[0] != "c1" {
		t.Fatalf("returned document aliases backend: %+v", backendDoc)
	}
}

func TestProjectPrefixStoreRejectsCompleteDocumentOutsideExpectedPath(t *testing.T) {
	for _, backendDoc := range []*store.Document{
		nil,
		{Path: "workspace/other/a.go", ChunkIDs: []string{"foreign"}},
		{Path: "workspace/project/other.go", ChunkIDs: []string{"wrong-file"}},
	} {
		backend := &completeDocumentStore{mockVectorStore: &mockVectorStore{}, doc: backendDoc}
		prefixed := &projectPrefixStore{store: backend, workspaceName: "workspace", projectName: "project"}
		got, err := prefixed.GetCompleteDocument(context.Background(), "a.go")
		if err != nil || got != nil {
			t.Fatalf("backend document %+v returned %+v, %v", backendDoc, got, err)
		}
	}
}

func TestProjectPrefixStoreCompleteDocumentUnsupportedWithoutFallback(t *testing.T) {
	backend := &mockVectorStore{getDocumentResult: &store.Document{Path: "metadata-only.go"}}
	prefixed := &projectPrefixStore{store: backend, workspaceName: "workspace", projectName: "project"}
	got, err := prefixed.GetCompleteDocument(context.Background(), "a.go")
	if got != nil || !errors.Is(err, store.ErrCompleteDocumentUnsupported) {
		t.Fatalf("GetCompleteDocument() = %+v, %v", got, err)
	}
	if backend.getDocumentPath != "" {
		t.Fatalf("metadata fallback called GetDocument(%q)", backend.getDocumentPath)
	}
}

func TestProjectPrefixStoreForwardsCompleteDocumentError(t *testing.T) {
	wantErr := errors.New("backend failure")
	backend := &completeDocumentStore{mockVectorStore: &mockVectorStore{}, err: wantErr}
	prefixed := &projectPrefixStore{store: backend, workspaceName: "workspace", projectName: "project"}
	if _, err := prefixed.GetCompleteDocument(context.Background(), "a.go"); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
