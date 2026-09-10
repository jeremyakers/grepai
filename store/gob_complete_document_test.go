package store

import (
	"context"
	"testing"
)

func TestGOBStoreGetCompleteDocument(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		doc    *Document
		chunks []Chunk
		want   bool
	}{
		{name: "absent"},
		{name: "zero IDs", doc: &Document{Path: "a.go"}},
		{
			name: "one of two missing",
			doc:  &Document{Path: "a.go", ChunkIDs: []string{"c1", "c2"}},
			chunks: []Chunk{
				{ID: "c1", FilePath: "a.go", Vector: []float32{1}},
			},
		},
		{
			name:   "wrong file",
			doc:    &Document{Path: "a.go", ChunkIDs: []string{"c1"}},
			chunks: []Chunk{{ID: "c1", FilePath: "other/a.go", Vector: []float32{1}}},
		},
		{
			name:   "cross-project identifier",
			doc:    &Document{Path: "a.go", ChunkIDs: []string{"workspace/two/a.go_0"}},
			chunks: []Chunk{{ID: "workspace/two/a.go_0", FilePath: "workspace/two/a.go", Vector: []float32{1}}},
		},
		{
			name:   "empty vector",
			doc:    &Document{Path: "a.go", ChunkIDs: []string{"c1"}},
			chunks: []Chunk{{ID: "c1", FilePath: "a.go"}},
		},
		{
			name: "complete",
			doc:  &Document{Path: "a.go", ChunkIDs: []string{"c1", "c2"}},
			chunks: []Chunk{
				{ID: "c1", FilePath: "a.go", Vector: []float32{1}},
				{ID: "c2", FilePath: "a.go", Vector: []float32{2}},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewGOBStore(t.TempDir() + "/index.gob")
			if err := st.SaveChunks(ctx, tt.chunks); err != nil {
				t.Fatal(err)
			}
			if tt.doc != nil {
				if err := st.SaveDocument(ctx, *tt.doc); err != nil {
					t.Fatal(err)
				}
			}
			got, err := st.GetCompleteDocument(ctx, "a.go")
			if err != nil || (got != nil) != tt.want {
				t.Fatalf("GetCompleteDocument() = %+v, %v; want present=%v", got, err, tt.want)
			}
			if tt.doc != nil {
				metadata, err := st.GetDocument(ctx, "a.go")
				if err != nil || metadata == nil {
					t.Fatalf("GetDocument metadata changed: %+v, %v", metadata, err)
				}
			}
			if got != nil {
				got.ChunkIDs[0] = "mutated"
				again, _ := st.GetCompleteDocument(ctx, "a.go")
				if again.ChunkIDs[0] != "c1" {
					t.Fatalf("returned chunk IDs alias store: %v", again.ChunkIDs)
				}
			}
		})
	}
}

func TestGOBStoreGetCompleteDocumentAfterReload(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/index.gob"
	st := NewGOBStore(path)
	if err := st.SaveChunks(ctx, []Chunk{{ID: "c1", FilePath: "a.go", Vector: []float32{1}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveDocument(ctx, Document{Path: "a.go", ChunkIDs: []string{"c1"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Persist(ctx); err != nil {
		t.Fatal(err)
	}
	reloaded := NewGOBStore(path)
	if err := reloaded.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.GetCompleteDocument(ctx, "a.go"); err != nil || got == nil {
		t.Fatalf("reloaded document = %+v, %v", got, err)
	}
}
