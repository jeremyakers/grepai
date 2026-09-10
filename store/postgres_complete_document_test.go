package store

import (
	"context"
	"testing"
	"time"
)

func TestPostgresGetCompleteDocumentValidation(t *testing.T) {
	st := newMetadataPostgresStore(t, nil)
	ctx := context.Background()
	other := &PostgresStore{pool: st.pool, projectID: "other-project", dimensions: 3}
	now := time.Now().UTC()

	saveChunk := func(target *PostgresStore, id, path string) {
		t.Helper()
		if err := target.SaveChunks(ctx, []Chunk{
			{ID: id, FilePath: path, Vector: []float32{1, 2, 3}, UpdatedAt: now},
		}); err != nil {
			t.Fatal(err)
		}
	}
	saveDoc := func(path string, ids ...string) {
		t.Helper()
		if ids == nil {
			ids = []string{}
		}
		if err := st.SaveDocument(ctx, Document{Path: path, ModTime: now, ChunkIDs: ids}); err != nil {
			t.Fatal(err)
		}
	}

	saveDoc("zero.go")
	saveChunk(st, "present", "partial.go")
	saveDoc("partial.go", "present", "missing")
	saveChunk(st, "wrong-file", "owner.go")
	saveDoc("wrong.go", "wrong-file")
	saveChunk(other, "foreign", "cross.go")
	saveDoc("cross.go", "foreign")
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO chunks (id, project_id, file_path, start_line, end_line, content, vector, hash, updated_at)
		VALUES ('null-vector', $1, 'null.go', 1, 1, '', NULL, '', $2)`, st.projectID, now); err != nil {
		t.Fatal(err)
	}
	saveDoc("null.go", "null-vector")
	saveChunk(st, "complete-1", "complete.go")
	saveChunk(st, "complete-2", "complete.go")
	saveDoc("complete.go", "complete-1", "complete-2")

	for _, path := range []string{"absent.go", "zero.go", "partial.go", "wrong.go", "cross.go", "null.go"} {
		t.Run(path, func(t *testing.T) {
			got, err := st.GetCompleteDocument(ctx, path)
			if err != nil || got != nil {
				t.Fatalf("GetCompleteDocument(%q) = %+v, %v; want nil, nil", path, got, err)
			}
			if path != "absent.go" {
				metadata, err := st.GetDocument(ctx, path)
				if err != nil || metadata == nil {
					t.Fatalf("GetDocument metadata changed: %+v, %v", metadata, err)
				}
			}
		})
	}

	got, err := st.GetCompleteDocument(ctx, "complete.go")
	if err != nil || got == nil || len(got.ChunkIDs) != 2 {
		t.Fatalf("complete document = %+v, %v", got, err)
	}
	got.ChunkIDs[0] = "mutated"
	again, err := st.GetCompleteDocument(ctx, "complete.go")
	if err != nil || again == nil || again.ChunkIDs[0] != "complete-1" {
		t.Fatalf("chunk IDs were not detached: %+v, %v", again, err)
	}
}
