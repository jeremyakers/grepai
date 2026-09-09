package cli

import (
	"context"
	"sync"
	"testing"

	"github.com/yoanbernabeu/grepai/store"
)

type countingNonBulkStore struct {
	*mockVectorStore
	documents map[string]store.Document
	mu        sync.Mutex
	gets      []string
}

func (s *countingNonBulkStore) GetDocument(_ context.Context, path string) (*store.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets = append(s.gets, path)
	doc, ok := s.documents[path]
	if !ok {
		return nil, nil
	}
	return &doc, nil
}

func TestProjectPrefixMetadataFallbackReadsOnlyCurrentProject(t *testing.T) {
	backend := &countingNonBulkStore{
		mockVectorStore: &mockVectorStore{listDocumentsResult: []string{
			"workspace/one/a.go",
			"workspace/one/b.go",
			"workspace/two/foreign.go",
		}},
		documents: map[string]store.Document{
			"workspace/one/a.go":       {Path: "workspace/one/a.go", Hash: "a"},
			"workspace/one/b.go":       {Path: "workspace/one/b.go", Hash: "b"},
			"workspace/two/foreign.go": {Path: "workspace/two/foreign.go", Hash: "foreign"},
		},
	}
	prefixed := &projectPrefixStore{store: backend, workspaceName: "workspace", projectName: "one"}
	metadata, err := prefixed.ListDocumentMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	gets := append([]string(nil), backend.gets...)
	backend.mu.Unlock()
	if len(metadata) != 2 || len(gets) != 2 {
		t.Fatalf("metadata=%v GetDocument calls=%v", metadata, gets)
	}
	for _, path := range gets {
		if path == "workspace/two/foreign.go" {
			t.Fatalf("foreign project point-read: %v", gets)
		}
	}
}
